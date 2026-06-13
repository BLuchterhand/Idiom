package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
)

func (r *Runner) implementationScopedDisallowedToolRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	var extReg *workspace.ExternalToolRegistry

	if r != nil && r.Workspace != nil {
		extReg = r.Workspace.ExternalToolsRegistry()
	}

	if lastResultSummaryIs(results, "Missing write description") {
		return "", "", "", false
	}

	if r == nil || !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) || implementationScopedEditToolAllowedWithRegistry(req.Tool, extReg) {
		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	summary = "Scoped implementation tool rejected"
	errorText = fmt.Sprintf("%s is outside the scoped required-path implementation surface for %s. The available contract outcomes are required-path write capability, semantic/context/declared project discovery for a named gap, a narrow raw read on a required path or evidence path, or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract menu is: required-path edit, context/language/declared read-only project discovery for the exact edit-safety fact, narrow raw read only for anchor confirmation on a required path or evidence path, or <<QUESTION>> with the exact blocker. Do not use task planning, shell, opaque external tools, or broad raw search until the required-path contract is resolved.", target)

	return summary, errorText, prompt, true
}

func (r *Runner) scopedImplementationLastChanceWriteOnlyApplies(policy RunPolicy, usedModelCalls int, results []workspace.ToolResult) bool {
	if r == nil || policy.MaxModelCalls <= 0 || usedModelCalls <= 0 {
		return false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return false
	}

	if !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}

	return usedModelCalls+1 >= policy.MaxModelCalls
}

func (r *Runner) scopedImplementationLastChanceWriteOnlyPrompt(results []workspace.ToolResult) string {
	target := "the remaining required edit path(s)"

	if r != nil {
		if paths := r.remainingRequiredEditPaths(results); len(paths) > 0 {
			target = strings.Join(paths, ", ")
		} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
			target = strings.Join(paths, ", ")
		}
	}

	return fmt.Sprintf("This is the final model-call budget slot for the scoped implementation run. Required edit paths remain unchanged: %s. Non-write tools are now hidden. Use one available parser-readable write tool for an in-scope required path from the packet and gathered source evidence, or answer exactly `<<QUESTION>>:` with the concrete path-specific blocker/no-op evidence. Do not read, search, inspect, or narrate a plan.", target)
}

func lastResultSummaryIs(results []workspace.ToolResult, summary string) bool {
	if len(results) == 0 {
		return false
	}

	return strings.TrimSpace(results[len(results)-1].Summary) == strings.TrimSpace(summary)
}

func (r *Runner) writeDescriptionRepairPrompt(req workspace.ToolRequest) string {
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		path = "the required edit path"
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the write tool"
	}

	return fmt.Sprintf("Write-description repair is active for %s on %s. Missing: a non-empty `description` field. Valid next actions: reissue the same in-scope write with `description`, or answer `<<QUESTION>>:` with the exact blocker. Invalid now: search, broad discovery, unrelated reads, unrelated writes, or prose final answer.", tool, path)
}

func (r *Runner) emitWriteDescriptionValidatorDecision(step int, req workspace.ToolRequest, status string, lastFailure string, recoveryID string) {
	if r == nil {
		return
	}

	path := normalizeRunnerPath(req.Path)
	payload := map[string]any{
		"schema":               "idiom.dynamic_validator_decision.v1",
		"step":                 step,
		"status":               strings.TrimSpace(status),
		"invariant_id":         "write.description.required",
		"recovery_id":          strings.TrimSpace(recoveryID),
		"last_failure":         strings.TrimSpace(lastFailure),
		"tool":                 strings.TrimSpace(req.Tool),
		"path":                 path,
		"missing":              []string{"write_description_missing"},
		"valid_next_actions":   []string{"same_write_with_description", "typed_blocker"},
		"invalid_next_actions": []string{"search", "broad_discovery", "unrelated_read", "unrelated_write", "prose_final"},
		"hide_tools":           []string{"non_write_tools"},
	}
	r.emitEvent(marshalRunEvent("dynamic_validator_decision", payload))
}

func (r *Runner) writeReadyPacketReadWithoutGapRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, hideNonWrite bool, reject bool) {
	if r == nil || !postReadinessToolContractForBrief(r.RuntimeBrief, results, false, false).RequireSourceRefreshContract || !r.scopedReadClassTool(req) {
		return "", "", "", false, false
	}

	hideNonWrite = implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results)

	missingGap := postReadinessGap(req) == ""
	// The read scope is the FULL required-edit-paths set plus explicit
	// packet evidence paths, not just the remaining edit paths. Once the
	// model has written one file in the brief, reads on that file are still
	// legitimate — the model often needs to reference its own header to write
	// the impl, verify a declaration it just landed, or mirror an existing
	// pattern from a sibling required file. Explicit evidence paths are also
	// legitimate read-only inputs (for example refine's previous draft/review
	// files). Tool-result evidence is intentionally not scope: context/query
	// packets can mention candidate or related files, and those candidates
	// must support reasoning without expanding the allowed read surface.
	readScope := implementationScopedExplicitReadScope(r.RuntimeBrief)
	outOfScope := implementationScopedEditSurfaceActive(r.RuntimeBrief, results) && !readRequestTargetsRequiredEditPath(req, readScope)

	if outOfScope && r.scopedImplementationDiscoveryLookupAllowed(req, readScope) {
		return "", "", "", false, false
	}

	// In-scope reads (target one of the brief's required_edit_paths) are
	// allowed without an explicit named_gap. The safety reason is
	// implicit — "verify the anchor on a path we're about to edit" — and
	// the schema describes named_gap as optional, so requiring it on
	// in-scope reads contradicted the surface the planner sees. Local
	// Qwen consistently calls code_navigate without named_gap and got
	// stuck; the previous behavior also rejected the asymmetric case
	// where read_file happened to include named_gap and code_navigate
	// didn't (Phase 6.5 / Phase 5b dispatch debugging, 2026-05-10).
	// Out-of-scope reads still get blocked regardless of named_gap —
	// that's the protection the gate is meant to provide.
	if !outOfScope {
		return "", "", "", false, false
	}

	scratchpadDeliverable := briefIsScratchpadDeliverableLane(r.RuntimeBrief)

	_ = missingGap // retained for the rejection-message switch below

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	summary = "Scoped implementation read rejected"

	switch {
	case missingGap && outOfScope:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must target a required edit path or explicit evidence path and include named_gap describing one exact edit-safety fact.", target)
	case missingGap:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must include named_gap describing one exact edit-safety fact.", target)
	default:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. This read/search targets outside the required edit paths and explicit evidence paths.", target)
	}

	if scratchpadDeliverable {
		prompt = fmt.Sprintf("The implementation packet defines a non-source deliverable for %s, and the attempted read/search was outside the required or evidence paths. Treat this as scope evidence for the deliverable: record the missing or out-of-scope path in a blocker or scope-correction section instead of broadening discovery.", target)
	} else if hideNonWrite {
		prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s, and the attempted read/search did not satisfy the contract. Read/search tools are now hidden for this turn. The available contract outcomes are the smallest safe write to each remaining required path, or <<QUESTION>> naming a stale or missing supplied anchor.", target)
	} else {
		prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract outcomes are an allowed edit on a required path, semantic/context/declared project discovery for a named edit-safety gap, one narrow raw read inside a required path or explicit evidence path for anchor confirmation, or <<QUESTION>> naming the blocker when a remaining path cannot be edited safely from the packet plus scoped evidence.", target)
	}

	return summary, errorText, prompt, hideNonWrite, true
}

func implementationScopedExplicitReadScope(brief workerruntime.WorkerRuntimeBrief) []string {
	return mergeRunnerStateStrings(brief.RequiredEditPaths, brief.EvidencePaths)
}

func (r *Runner) scratchpadDeliverableFailedEvidenceNudge(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || strings.TrimSpace(result.Error) == "" {
		return "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || !r.scopedReadClassTool(req) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	return fmt.Sprintf("The optional evidence request for %s failed: %s. For this non-source deliverable, treat that as an evidence fact: record the missing, stale, ambiguous, or out-of-scope evidence in the deliverable itself rather than repairing the lookup through another raw or lower-level read. The next action should be either writing the deliverable with the evidence gap recorded, or answering <<QUESTION>> if the gap makes the deliverable impossible.", target, reason), true
}

func scratchpadDeliverableFailedEvidenceResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if !briefIsScratchpadDeliverableLane(brief) {
		return workspace.ToolResult{}, false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
		return workspace.ToolResult{}, false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" || workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		if strings.TrimSpace(result.Summary) == "Scratchpad evidence follow-up rejected" {
			continue
		}

		if isReadClassTool(workspace.ToolRequest{Tool: result.Tool}) {
			return result, true
		}
	}

	return workspace.ToolResult{}, false
}

func scratchpadDeliverableFailedEvidenceStatus(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	failed, ok := scratchpadDeliverableFailedEvidenceResult(brief, results)
	if !ok {
		return ""
	}

	target := "the required deliverable"
	if missing := missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(failed.Error)
	if reason == "" {
		reason = strings.TrimSpace(failed.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	tool := strings.TrimSpace(failed.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}

	return fmt.Sprintf("closed_negative_evidence: optional %s evidence for %s failed (%s); record this as an evidence gap in the non-source deliverable if the exact source fact remains unavailable", tool, target, util.Truncate(strings.TrimSpace(reason), 220))
}

func (r *Runner) scopedReadClassTool(req workspace.ToolRequest) bool {
	if isReadClassTool(req) {
		return true
	}

	return r != nil && r.externalReadOnlyDiscoveryTool(req.Tool)
}

func (r *Runner) externalReadOnlyDiscoveryTool(tool string) bool {
	if r == nil || r.Workspace == nil {
		return false
	}

	extReg := r.Workspace.ExternalToolsRegistry()

	return extReg != nil && extReg.IsReadOnlyDiscovery(tool)
}

func (r *Runner) scopedImplementationDiscoveryLookupAllowed(req workspace.ToolRequest, pathScope []string) bool {
	if !scopedImplementationDiscoveryPathAllowed(req, pathScope) {
		return false
	}

	if r != nil && r.externalReadOnlyDiscoveryTool(req.Tool) {
		return true
	}

	if scopedImplementationSymbolOwnerLookupAllowed(req) {
		return true
	}

	if !implementationScopedSemanticDiscoveryToolAllowed(req.Tool) {
		return false
	}

	if strings.TrimSpace(req.Tool) == workspace.ToolNameContextStatus {
		return true
	}

	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.Path, req.Command))
	if target == "" || len(target) < 3 {
		return false
	}

	profile := workspace.DiscoveryProfileForRequest(req)
	if profile.Scope == "broad" || profile.Cost == "high" {
		return false
	}

	return true
}

func (r *Runner) writeOnlyRecoveryToolIsWrite(tool string) bool {
	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	if r == nil || strings.TrimSpace(tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func scopedImplementationDiscoveryPathAllowed(req workspace.ToolRequest, pathScope []string) bool {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return true
	}

	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	for _, scoped := range pathScope {
		scoped = normalizeRunnerPath(scoped)
		if scoped == "" {
			continue
		}

		if normalized == scoped {
			return true
		}

		if !runnerPathLooksLikeFile(scoped) && strings.HasPrefix(normalized, scoped+"/") {
			return true
		}
	}

	return scopedImplementationPackageDiscoveryPathAllowed(req, normalized, pathScope)
}

func runnerPathLooksLikeFile(path string) bool {
	base := filepath.Base(strings.TrimSpace(filepath.ToSlash(path)))
	return strings.Contains(base, ".")
}

func scopedImplementationPackageDiscoveryPathAllowed(req workspace.ToolRequest, normalized string, pathScope []string) bool {
	normalized = normalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(normalized)))
	if normalized == "" || normalized == "." || normalized == "/" || runnerPathLooksLikeFile(normalized) {
		return false
	}

	if !scopedImplementationDirectoryDiscoveryHasExactTarget(req) {
		return false
	}

	for _, scoped := range pathScope {
		scoped = normalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(scoped)))
		if scoped == "" || !runnerPathLooksLikeFile(scoped) {
			continue
		}

		parent := normalizeRunnerPath(filepath.ToSlash(filepath.Dir(scoped)))
		if parent == "" || parent == "." || parent == "/" {
			continue
		}

		if normalized == parent {
			return true
		}
	}

	return false
}

func scopedImplementationDirectoryDiscoveryHasExactTarget(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.NamedGap))
	if target == "" || len(target) < 3 {
		return false
	}

	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}

	if strings.Contains(target, "$") {
		return false
	}

	return true
}

func scopedImplementationSymbolOwnerLookupAllowed(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability {
		capability := strings.ToLower(strings.TrimSpace(req.Capability))
		switch capability {
		case "find_symbol", "definition", "declaration", "workspace_symbol", "symbol", "symbol-lookup", "symbol_lookup":
		default:
			return false
		}

		return scopedImplementationLookupTargetAllowed(req)
	}

	_, suffix, ok := runnerLanguageToolParts(tool)
	if !ok {
		return false
	}

	switch suffix {
	case "find_symbol", "find_function", "find_type", "find_const", "find_package",
		"definition", "declaration", "workspace_symbol", "read_symbol":
	default:
		return false
	}

	return scopedImplementationLookupTargetAllowed(req)
}

func scopedImplementationLookupTargetAllowed(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.ID, req.Handle))
	if target == "" || len(target) < 3 {
		return false
	}

	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}

	return true
}

func runnerLanguageToolParts(tool string) (language string, suffix string, ok bool) {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			return strings.TrimSuffix(prefix, "_"), strings.TrimPrefix(tool, prefix), true
		}
	}

	return "", "", false
}

// writeOnlyRecoveryBlockerAnswer returns the <<QUESTION>> answer the runner
// emits when write-only recovery is active and the model tried a non-write
// tool that the bounded read allowance can no longer cover. Returns ok=false
// when the situation doesn't warrant a recovery blocker (e.g. read-only
// lane, no required edit paths remain).
func (r *Runner) writeOnlyRecoveryBlockerAnswer(results []workspace.ToolResult) (string, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and required edit path(s) remain unchanged: %s. Complete those edits from the current packet/evidence, or re-dispatch with a narrower anchor for each skipped path.", strings.Join(missing, ", ")), true
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}

	target := "the requested implementation target"
	if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and no edit has been attempted. Provide a narrower target packet or preloaded current-state anchor for %s; no workspace change was made.", target), true
}

func (r *Runner) writeOnlyRecoveryBlockerKind(results []workspace.ToolResult) string {
	if r != nil && len(r.remainingRequiredEditPaths(results)) > 0 {
		return "write_only_recovery_required_paths_remaining"
	}

	return "write_only_recovery_no_edit_attempted"
}

func (r *Runner) writeOnlyRecoveryNonWriteRepairPrompt(req workspace.ToolRequest, results []workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested non-write tool"
	}

	lines := []string{
		fmt.Sprintf("%s was rejected because write-only recovery is active after repeated no-write completion attempts.", tool),
		"Do not read, search, navigate, inspect, summarize, or plan again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		lines = append(lines, "Required edit path(s) still unchanged: "+strings.Join(missing, ", ")+".")
	}

	lines = append(lines, "Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func (r *Runner) remainingRequiredEditPaths(results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	return missingRequiredEditPaths(r.requiredEditCoveragePaths(), changedPathsFromToolResults(results))
}

func (r *Runner) requiredEditCoveragePaths() []string {
	if r == nil {
		return nil
	}

	required := r.RuntimeBrief.RequiredEditPaths
	if !r.planThenExecutePlanSubmissionPhase() {
		return required
	}

	root := ""
	if r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}

	return planThenExecuteRequiredCoveragePaths(required, root)
}

// applyPatchPreflightAction describes what the runner should do with an
// apply_patch request after the changed-paths pre-flight has inspected it.
func contextPackUnavailableForRecovery(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return true
		}

		if !contextPackResultHasTargets(result) {
			return true
		}
	}

	return false
}

func (r *Runner) recoverContextPackFreshness(ctx context.Context, req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameContextPack || !contextPackResultNeedsFreshnessRecovery(result) {
		return result
	}

	r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_pack freshness blocked; refreshing context status before retry"})

	refresh := workspace.ToolRequest{
		Tool:              workspace.ToolNameContextStatus,
		Refresh:           true,
		Limit:             50,
		FreshnessMaxFiles: 2000,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	}
	if strings.TrimSpace(req.Language) != "" {
		refresh.Language = req.Language
	}

	refreshResult := r.Workspace.Execute(ctx, refresh)
	if summary := strings.TrimSpace(util.FirstNonEmpty(refreshResult.Summary, refreshResult.Error)); summary != "" {
		r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_status refresh result: " + util.Truncate(strings.TrimSpace(summary), 260)})
	}

	retry := r.Workspace.Execute(ctx, req)
	if strings.TrimSpace(retry.Error) == "" || strings.TrimSpace(refreshResult.Error) == "" {
		return retry
	}

	return result
}

func contextPackResultNeedsFreshnessRecovery(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "freshness_blocker" {
		return true
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))

	return strings.Contains(text, "freshness_blocker") ||
		strings.Contains(text, "stale_unbounded_graph") ||
		strings.Contains(text, "context freshness")
}

func contextPackSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
			return true
		}
	}

	return false
}

func contextPackResultHasTargets(result workspace.ToolResult) bool {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return false
	}

	var view struct {
		CandidateFiles   []json.RawMessage `json:"candidate_files"`
		CandidateSymbols []json.RawMessage `json:"candidate_symbols"`
		Tests            []json.RawMessage `json:"tests"`
	}

	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return false
	}

	return len(view.CandidateFiles) > 0 || len(view.CandidateSymbols) > 0 || len(view.Tests) > 0
}

// contextPackCandidateSymbols extracts the top-ranked symbol references from
// a context_pack view JSON. It prefers top-level candidate_symbols and packet
// quality evidence, then falls back to candidate_files.handles from older views.
func contextPackCandidateSymbols(output string, max int) []string {
	output = strings.TrimSpace(output)
	if output == "" || max <= 0 {
		return nil
	}

	var view struct {
		Task    string `json:"task"`
		Quality struct {
			EvidencePaths []string `json:"evidence_paths"`
		} `json:"quality"`
		CandidateFiles []struct {
			Path    string   `json:"path"`
			Role    string   `json:"role"`
			Reason  string   `json:"reason"`
			Handles []string `json:"handles"`
		} `json:"candidate_files"`
		CandidateSymbols []struct {
			Path          string `json:"path"`
			Kind          string `json:"kind"`
			QualifiedName string `json:"qualified_name"`
			Reason        string `json:"reason"`
		} `json:"candidate_symbols"`
	}

	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return nil
	}

	evidenceRank := map[string]int{}

	for i, path := range view.Quality.EvidencePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if _, ok := evidenceRank[path]; !ok {
			evidenceRank[path] = i
		}
	}

	tokens := runnerEvidenceTokens(view.Task)
	candidates := make([]contextPackSymbolCandidate, 0, len(view.CandidateSymbols))

	for _, symbol := range view.CandidateSymbols {
		path := strings.TrimSpace(symbol.Path)
		name := strings.TrimSpace(symbol.QualifiedName)

		if path == "" || name == "" {
			continue
		}

		candidates = append(candidates, contextPackSymbolCandidate{
			value:        path + "#" + name,
			path:         path,
			evidenceRank: contextPackEvidenceRank(evidenceRank, path),
			score:        runnerEvidenceOverlap(tokens, strings.Join([]string{path, symbol.Kind, name, symbol.Reason}, " ")),
		})
	}

	sortContextPackSymbolCandidates(candidates)

	var symbols []string

	seen := map[string]struct{}{}
	addSymbol := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(symbols) >= max {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		symbols = append(symbols, value)
	}

	for _, candidate := range candidates {
		addSymbol(candidate.value)
	}

	for _, f := range view.CandidateFiles {
		if len(symbols) >= max {
			break
		}

		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}

		if strings.TrimSpace(f.Role) == "test" {
			continue
		}

		for _, handle := range f.Handles {
			if len(symbols) >= max {
				break
			}

			handle = strings.TrimPrefix(strings.TrimSpace(handle), "symbol://")
			if handle == "" || handle == path {
				continue
			}

			addSymbol(handle)
		}
	}

	if len(symbols) == 0 {
		return nil
	}

	return symbols
}

type contextPackSymbolCandidate struct {
	value        string
	path         string
	evidenceRank int
	score        int
}

func contextPackEvidenceRank(ranks map[string]int, path string) int {
	if len(ranks) == 0 {
		return -1
	}

	if rank, ok := ranks[strings.TrimSpace(path)]; ok {
		return rank
	}

	return -1
}

func sortContextPackSymbolCandidates(candidates []contextPackSymbolCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].evidenceRank >= 0 || candidates[j].evidenceRank >= 0 {
			if candidates[i].evidenceRank < 0 {
				return false
			}

			if candidates[j].evidenceRank < 0 {
				return true
			}

			if candidates[i].evidenceRank != candidates[j].evidenceRank {
				return candidates[i].evidenceRank < candidates[j].evidenceRank
			}
		}

		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}

		return candidates[i].value < candidates[j].value
	})
}

func concreteRecoveryEvidencePaths(results []workspace.ToolResult, max int) []string {
	filtered := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameContextPack, "completion_gate":
			continue
		default:
			filtered = append(filtered, result)
		}
	}

	return promptEvidencePathsFromToolResults(filtered, max)
}

func discoveryConsumptionCandidatesFromResult(result workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	if strings.TrimSpace(result.Error) != "" {
		return nil
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, max)
	}

	return promptEvidencePathsFromToolResults([]workspace.ToolResult{result}, max)
}

func recoveryEvidencePathsForObjective(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	searchRanked := rankedSearchContentEvidencePaths(objective, results, max)
	if len(searchRanked) >= max {
		return searchRanked[:max]
	}

	seen := make(map[string]struct{}, len(searchRanked))
	paths := make([]string, 0, max)

	for _, path := range searchRanked {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range concreteRecoveryEvidencePaths(results, max*2) {
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		if len(paths) >= max {
			break
		}
	}

	return paths
}

func candidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := make(map[string]struct{}, max)
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" {
			return
		}

		if !planThenExecuteRecoveryScopePathAllowed(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range brief.AllowedEditPaths {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range recoveryEvidencePathsForObjective(brief.Objective, results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range contextPackCandidatePathsFromResults(results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	return paths
}

func contextPackCandidatePathsFromResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	addRanked := func(candidates []contextPackEvidenceCandidate) bool {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}

			if candidates[i].source != candidates[j].source {
				return candidates[i].source
			}

			return candidates[i].path < candidates[j].path
		})

		for _, candidate := range candidates {
			if candidate.score <= 0 {
				continue
			}

			add(candidate.path)

			if len(paths) >= max {
				return true
			}
		}

		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		var view struct {
			Task    string `json:"task"`
			Quality struct {
				LocalizationConfidence string   `json:"localization_confidence"`
				SourceOwnerFound       bool     `json:"source_owner_found"`
				EvidencePaths          []string `json:"evidence_paths"`
			} `json:"quality"`
			CandidateFiles []struct {
				Path    string   `json:"path"`
				Role    string   `json:"role"`
				Reason  string   `json:"reason"`
				Handles []string `json:"handles"`
			} `json:"candidate_files"`
			CandidateSymbols []struct {
				Path          string `json:"path"`
				Kind          string `json:"kind"`
				QualifiedName string `json:"qualified_name"`
				Reason        string `json:"reason"`
			} `json:"candidate_symbols"`
			Tests []struct {
				Path    string `json:"path"`
				Symbol  string `json:"symbol"`
				Command string `json:"command"`
				Reason  string `json:"reason"`
			} `json:"tests"`
		}

		if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
			continue
		}

		weakQuality := strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
		for _, path := range view.Quality.EvidencePaths {
			if weakQuality && !runnerEvidencePathIsTest(path) {
				continue
			}

			add(path)

			if len(paths) >= max {
				return paths
			}
		}

		if tokens := runnerEvidenceTokens(view.Task); len(tokens) > 0 {
			candidates := make([]contextPackEvidenceCandidate, 0, len(view.CandidateFiles)+len(view.CandidateSymbols)+len(view.Tests))
			for _, file := range view.CandidateFiles {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   file.Path,
					source: !runnerEvidencePathIsTest(file.Path),
					score:  runnerEvidenceOverlap(tokens, strings.Join(append([]string{file.Path, file.Role, file.Reason}, file.Handles...), " ")),
				})
			}

			for _, symbol := range view.CandidateSymbols {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   symbol.Path,
					source: !runnerEvidencePathIsTest(symbol.Path) && symbol.Kind != "test",
					score:  runnerEvidenceOverlap(tokens, strings.Join([]string{symbol.Path, symbol.Kind, symbol.QualifiedName, symbol.Reason}, " ")),
				})
			}

			for _, test := range view.Tests {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:  test.Path,
					score: runnerEvidenceOverlap(tokens, strings.Join([]string{test.Path, test.Symbol, test.Command, test.Reason}, " ")),
				})
			}

			if addRanked(candidates) {
				return paths
			}
		}

		for _, file := range view.CandidateFiles {
			add(file.Path)

			if len(paths) >= max {
				return paths
			}
		}

		for _, symbol := range view.CandidateSymbols {
			add(symbol.Path)

			if len(paths) >= max {
				return paths
			}
		}

		for _, test := range view.Tests {
			add(test.Path)

			if len(paths) >= max {
				return paths
			}
		}
	}

	return paths
}

type contextPackEvidenceCandidate struct {
	path   string
	source bool
	score  int
}

func contextPackResultWeakSourceOwner(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
		return false
	}

	var view struct {
		Quality struct {
			LocalizationConfidence string `json:"localization_confidence"`
			SourceOwnerFound       bool   `json:"source_owner_found"`
		} `json:"quality"`
	}

	if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
		return false
	}

	return strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
}

func runnerEvidenceTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, token := range splitRunnerEvidenceText(text) {
		if len(token) < 3 || runnerEvidenceStopToken(token) {
			continue
		}

		out[runnerEvidenceStem(token)] = struct{}{}
	}

	return out
}

func runnerEvidenceOverlap(tokens map[string]struct{}, text string) int {
	seen := map[string]struct{}{}

	for _, token := range splitRunnerEvidenceText(text) {
		stem := runnerEvidenceStem(token)
		if _, ok := tokens[stem]; ok {
			seen[stem] = struct{}{}
		}
	}

	return len(seen)
}

func splitRunnerEvidenceText(text string) []string {
	var (
		tokens []string
		b      strings.Builder
	)

	flush := func() {
		if b.Len() == 0 {
			return
		}

		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}

	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			flush()
			b.WriteRune(r + ('a' - 'A'))

			continue
		}

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			continue
		}

		flush()
	}

	flush()

	return tokens
}

func runnerEvidenceStem(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return runnerEvidenceTrimDoubledFinalConsonant(strings.TrimSuffix(token, suffix))
		}
	}

	return token
}

func runnerEvidenceTrimDoubledFinalConsonant(token string) string {
	if len(token) < 2 {
		return token
	}

	last := token[len(token)-1]
	prev := token[len(token)-2]

	if last == prev {
		switch last {
		case 'b', 'd', 'g', 'm', 'n', 'p', 'r', 't':
			return token[:len(token)-1]
		}
	}

	return token
}

func runnerEvidenceStopToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "where", "when", "that", "this", "from", "into", "over",
		"do", "not", "no", "only", "same", "way", "longer", "first", "later", "time",
		"bug", "fix", "issue", "fail", "fails", "failure", "source", "file", "files", "symbol", "symbols",
		"research", "repo", "repository", "edit", "edits", "editing", "investigate", "identify", "name",
		"likely", "smallest", "proper", "propose", "return", "answer", "evidence", "codebase", "exact",
		"missing", "insufficient", "sufficient", "claim":
		return true
	default:
		return false
	}
}

func runnerEvidencePathIsTest(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))

	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/fixtures/") ||
		strings.Contains(path, "/tests/")
}

func unreadCandidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := readPathsFromResults(results)
	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; ok {
			return nil
		}
	}

	unread := make([]string, 0, len(candidates))

	unread = append(unread, candidates...)

	return unread
}

func unreadCandidateRecoveryScopePathsForRecovery(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := readPathsFromResults(results)
	unread := make([]string, 0, len(candidates))

	for _, path := range candidates {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if candidateRecoveryScopePathHasMissingFileEvidence(normalized, results) {
			continue
		}

		if _, ok := read[normalized]; ok {
			continue
		}

		unread = append(unread, normalized)
	}

	return unread
}

func candidateRecoveryScopePathHasMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	lowerPath := strings.ToLower(normalized)
	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.Contains(text, "file not found") ||
			strings.Contains(text, "no such file") ||
			strings.Contains(text, "cannot find the file") ||
			strings.Contains(text, "does not exist") {

			return true
		}
	}

	return false
}

func rankedSearchContentEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := recoveryObjectiveSignalTerms(objective)
	lowerObjective := strings.ToLower(objective)
	uiObjective := objectiveMentionsUI(lowerObjective)

	type scoredPath struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scoredPath{}
	order := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range strings.Split(result.Output, "\n") {
			path, text, ok := searchContentPathAndText(line)
			if !ok {
				continue
			}

			entry, exists := scores[path]
			if !exists {
				entry = &scoredPath{path: path, order: order}
				scores[path] = entry
				order++
			}

			entry.score += recoverySearchLineScore(path, text, terms, uiObjective)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	paths := make([]scoredPath, 0, len(scores))
	for _, entry := range scores {
		paths = append(paths, *entry)
	}

	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].score != paths[j].score {
			return paths[i].score > paths[j].score
		}

		return paths[i].order < paths[j].order
	})

	out := make([]string, 0, min(max, len(paths)))
	for _, entry := range paths {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func searchContentPathAndText(line string) (path string, text string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", "", false
	}

	before := strings.TrimSpace(line[:firstColon])
	fields := strings.Fields(before)

	if len(fields) == 0 {
		return "", "", false
	}

	path = fields[len(fields)-1]
	if !recoverySearchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")

	if secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return filepath.ToSlash(path), text, true
}

func recoverySearchLineScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "tool_schemas.go") || strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if strings.HasPrefix(lowerPath, "cmd/") && !containsAny(lowerText, "implementation", "core", "internal") {
		score -= 20
	}

	if slices.Contains(terms, "apply") && containsAny(lowerPath, "migration_apply", "_apply.", "/apply") {
		score += 60
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if containsAny(lowerPath, "render", "view", "model_state", "update_handlers") {
			score += 25
		}

		if containsAny(lowerPath, "frontier/", "comprehension/", "workspace/") {
			score -= 25
		}
	}

	return score
}

func recoveryObjectiveSignalTerms(objective string) []string {
	pattern := literalRecoverySearchPattern(objective)
	if pattern == "" {
		return nil
	}

	parts := strings.Split(pattern, "|")
	terms := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}

		terms = append(terms, strings.ReplaceAll(part, `\`, ""))
	}

	return terms
}

func objectiveMentionsUI(lowerObjective string) bool {
	return containsAny(lowerObjective, "tui", "ui", "banner", "header", "footer", "top-right", "top right", "window", "screen", "view", "render")
}
