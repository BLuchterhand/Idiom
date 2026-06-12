package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"strings"
)

type applyPatchPreflightAction string

const (
	// applyPatchPreflightPassthrough means the request can flow to the
	// real applyPatch unchanged.
	applyPatchPreflightPassthrough applyPatchPreflightAction = "passthrough"
	// applyPatchPreflightRewrite means the patch had at least one section
	// targeting a path already in changed_paths; those sections were
	// dropped and the runner should send the trimmed envelope to applyPatch.
	applyPatchPreflightRewrite applyPatchPreflightAction = "rewrite"
	// applyPatchPreflightAllSkipped means EVERY section in the patch
	// targeted an already-changed path. The runner short-circuits with a
	// synthesized success result instead of running an empty patch.
	applyPatchPreflightAllSkipped applyPatchPreflightAction = "all_skipped"
)

// applyPatchPreflightResult carries the pre-flight verdict back to the
// runner loop.
type applyPatchPreflightResult struct {
	Action       applyPatchPreflightAction
	Request      workspace.ToolRequest
	Result       workspace.ToolResult
	SkippedPaths []string
}

// preflightApplyPatchAgainstChangedPaths inspects an apply_patch request
// against the lane's recorded changed paths. The model commonly issues a
// multi-file patch that includes a section for a file it already wrote
// via a prior edit_file/apply_patch — the previous edit makes that
// section's hunk context stale, so applyPatch's all-or-nothing semantics
// would roll back the entire envelope including the still-valid sections
// for the remaining required paths.
//
// The pre-flight strips any Update/Delete section targeting an
// already-changed path and re-submits the trimmed envelope. If every
// section was redundant the runner skips the workspace round-trip and
// emits a success result noting which paths were already applied. If
// nothing is redundant the request flows through unchanged.
//
// This compatibility shim does not change applyPatch's semantics; it just
// drops sections that would silently fail under the existing all-or-
// nothing contract because the file is already in changed_paths.
func (r *Runner) preflightApplyPatchAgainstChangedPaths(req workspace.ToolRequest, results []workspace.ToolResult) applyPatchPreflightResult {
	pass := applyPatchPreflightResult{Action: applyPatchPreflightPassthrough, Request: req}
	if r == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameApplyPatch {
		return pass
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return pass
	}

	ops, err := workspace.ParsePatch(content)
	if err != nil {
		// Let the real applyPatch surface the parse error so the model
		// gets the actual envelope-grammar problem.
		return pass
	}

	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return pass
	}

	var (
		keptOps []workspace.PatchOp
		skipped []string
	)

	for _, op := range ops {
		if op.Kind != workspace.PatchOpUpdate && op.Kind != workspace.PatchOpDelete {
			keptOps = append(keptOps, op)
			continue
		}

		path := normalizeRunnerPath(op.Path)
		if path == "" {
			keptOps = append(keptOps, op)
			continue
		}

		if _, ok := changed[path]; ok {
			skipped = append(skipped, op.Path)
			continue
		}

		keptOps = append(keptOps, op)
	}

	if len(skipped) == 0 {
		return pass
	}

	if len(keptOps) == 0 {
		// Every section in the patch was a redundant touch on an
		// already-changed path. Skip the workspace call entirely and
		// report this as a no-op patch (no new changes) so the model
		// cannot confuse the synthesized result with completion. The
		// summary and output explicitly state that NO new files were
		// modified and list the remaining required paths so the model
		// has a directional next step.
		remaining := r.remainingRequiredEditPaths(results)

		var output strings.Builder

		output.WriteString("apply_patch made NO new changes — every section in this envelope targets a path already modified earlier in this lane (")
		output.WriteString(strings.Join(skipped, ", "))
		output.WriteString("), so the patch was a redundant restatement of work already done.")

		switch {
		case len(remaining) > 0:
			output.WriteString(" The implementation contract is NOT yet fulfilled: required edit path(s) ")
			output.WriteString(strings.Join(remaining, ", "))
			output.WriteString(" are still unchanged. The remaining-path write contract requires a fresh edit covering ONLY those paths; do not declare completion until each is written.")
		case len(r.RuntimeBrief.RequiredEditPaths) > 0:
			output.WriteString(" All required edit paths are already in changed_paths from earlier writes; the implementation contract is fulfilled. Submit the standard final answer with the changed-paths summary instead of any further patches.")
		}

		return applyPatchPreflightResult{
			Action:       applyPatchPreflightAllSkipped,
			Request:      req,
			SkippedPaths: skipped,
			Result: workspace.ToolResult{
				Tool:    workspace.ToolNameApplyPatch,
				Summary: "apply_patch made no new changes (all sections were redundant)",
				Output:  output.String(),
			},
		}
	}

	rewritten := req

	rewritten.Content = workspace.SerializePatch(keptOps)

	return applyPatchPreflightResult{
		Action:       applyPatchPreflightRewrite,
		Request:      rewritten,
		SkippedPaths: skipped,
	}
}

// shouldForceAnswerForPostCompletionVerification returns true when the
// implementation contract is already fulfilled (all required edit paths
// have been written) and the agent is now thrashing on non-write tool
// calls to self-verify its work. Counts the current request if it is a
// non-write tool, then returns true when the running counter exceeds the
// configured cutoff. Write tools never count and reset the cutoff in
// effect because a write means the agent is actually doing more work, not
// just verifying.
func (r *Runner) shouldForceAnswerForPostCompletionVerification(req workspace.ToolRequest, results []workspace.ToolResult, counter *int) bool {
	if r == nil || counter == nil {
		return false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return false
	}

	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {

		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	*counter++

	return *counter > postCompletionVerificationCutoff
}

// consumeRecoveryReadAllowance returns true and marks the path consumed if
// this request is a bounded read on a remaining required edit path that has
// not yet used its one-read recovery allowance. Only read_file qualifies —
// search/list/navigation tools are deliberately not permitted in recovery
// because they invite further unbounded discovery.
func (r *Runner) consumeRecoveryReadAllowance(req workspace.ToolRequest, used map[string]bool, results []workspace.ToolResult) bool {
	if r == nil || used == nil {
		return false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if used[path] {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	// Allow read_file on remaining required edit paths (existing behavior).
	if tool == workspace.ToolNameReadFile {
		remaining := r.remainingRequiredEditPaths(results)
		if requestPathInRequiredEditPaths(req.Path, remaining) {
			used[path] = true
			return true
		}

		return false
	}

	// Allow code_navigate on pending obligation paths so write-only recovery
	// does not trap the model with an obligation that only code_navigate can
	// resolve.
	if tool == workspace.ToolNameCodeNavigate {
		pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results)
		for _, ob := range pending {
			if normalizeRunnerPath(ob.Path) == path {
				used[path] = true
				return true
			}
		}
	}

	return false
}

func (r *Runner) exactAnchorHiddenToolBlockerAnswer(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	if r == nil || !exactAnchorImplementationActive(r.RuntimeBrief) {
		return "", false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}

	target := "the requested exact-anchor implementation target"
	if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "hidden tool"
	}

	return fmt.Sprintf("<<QUESTION>>: Exact-anchor implementation did not complete because %s was repeatedly requested before applying the supplied write anchor for %s. No workspace change was made. Re-dispatch with the same exact anchor as a write-capability request, or provide a narrower current-state packet if the anchor is stale.", tool, target), true
}

func (r *Runner) exactAnchorRepeatedWriteFailureBlockerAnswer(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !exactAnchorImplementationActive(r.RuntimeBrief) {
		return "", false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}

	target := "the requested exact-anchor implementation target"
	if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "write tool"
	}

	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}

	if reason == "" {
		reason = "the corrected write did not satisfy the active exact-anchor contract"
	}

	return fmt.Sprintf("<<QUESTION>>: Exact-anchor implementation did not complete because repeated %s write attempts for %s were rejected after the recovery nudge. No workspace change was made. Last rejection: %s", tool, target, reason), true
}
