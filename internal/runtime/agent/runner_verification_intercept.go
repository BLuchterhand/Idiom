package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/agentutil"
)

// verificationInterceptResult tells the runner loop whether a read-class
// request should bypass the workspace dispatch with a synthesized
// redirect message.
type verificationInterceptResult struct {
	// Intercepted is true when the request was redirected. The runner
	// should use Result instead of calling Workspace.Execute.
	Intercepted bool
	// Result is the synthesized tool result the runner should record.
	// Carries an error string with the directive message and a summary
	// the audit log can read at a glance.
	Result workspace.ToolResult
	// Reason is a short human-readable string for telemetry events
	// (e.g. "read of already-edited path; redirected to next required").
	Reason string
}

// preflightVerificationOnAlreadyEditedPath intercepts read-class tool
// calls aimed at a required_edit_path that has already been written
// successfully when other ✗ required paths remain. The model's most
// common stall mode after the first few edits is to enter "verify each
// edit landed" mode and never return to writing the remaining paths.
// Each verification read uses up step budget without progressing the
// brief; eventually the doom-loop guard fires and the lane closes with
// half the work done.
//
// The intercept replaces the tool result with a one-line redirect that
// names the next pending path and forbids re-verification. The model
// then reads the directive on the next turn and (in practice) issues
// an edit_file against the named path.
//
// Conditions for intercept:
//   - The brief has required_edit_paths set.
//   - At least one required path is still pending (✗).
//   - The request is a read-class tool with a path that resolves to a
//     required_edit_path already on the ✓ list.
//   - The immediately preceding tool result was ALSO a read-class
//     tool. The first read after a write is left through because the
//     model legitimately uses "write a, read a, write B" to mirror
//     a freshly-landed structure into the next path. The second
//     read in a row is the verification-spree pattern.
//
// search_content and search_files are deliberately exempt: they are
// the right way to verify a literal expression inside a file, and
// blocking them would push the model back to read_symbol prose calls
// (the failure mode the read_symbol pattern validator already
// handles).
func (r *Runner) preflightVerificationOnAlreadyEditedPath(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil {
		return verificationInterceptResult{}
	}

	required := r.RuntimeBrief.RequiredEditPaths
	if len(required) == 0 {
		return verificationInterceptResult{}
	}

	if !verificationInterceptToolCovered(req) {
		return verificationInterceptResult{}
	}

	if !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return verificationInterceptResult{}
	}

	target := agentutil.NormalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return verificationInterceptResult{}
	}

	changed := changedPathsFromToolResults(results)
	if _, ok := changed[target]; !ok {
		return verificationInterceptResult{}
	}

	// Only intercept paths that are part of the required edit set —
	// reads of unrelated already-edited files are usually legitimate
	// (cross-file context, includes, etc.).
	if !requiredEditPathsContains(required, target) {
		return verificationInterceptResult{}
	}

	pending := missingRequiredEditPaths(required, changed)
	if len(pending) == 0 {
		// Nothing left to write; let normal completion path handle the
		// final answer. Re-verification at this stage is harmless.
		return verificationInterceptResult{}
	}

	if !precedingResultWasReadClass(results) {
		// First read after a write — could be legitimate prep for the
		// next ✗ path (mirror existing structure into the new file).
		// Let it through; only intercept when the model has stacked
		// reads in a row.
		return verificationInterceptResult{}
	}

	directive := verificationInterceptDirective(req.Tool, target, pending)
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Read of already-edited required path redirected to remaining write",
		Error:   directive,
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result:      result,
		Reason: fmt.Sprintf(
			"read on ✓ %s suppressed; %d required path(s) still pending; next: %s",
			target, len(pending), pending[0],
		),
	}
}

// verificationInterceptToolCovered restricts the intercept to the
// read-class tools the model uses for per-edit verification. We
// deliberately leave search_content / search_files / discovery_query
// alone — those are aimed at literal text or evidence gathering and
// have legitimate use cases against already-edited paths.
//
// For code_navigate, only the file_outline and read_symbol
// capabilities trip the intercept — they're the verification habits
// from the donkey-run failure pattern. find_symbol / references /
// definition / calls / implementations / related_files are lookup
// tools that have real value on already-edited paths (e.g., "where
// is X used now that I added it") and are left alone.
func verificationInterceptToolCovered(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return true
	case "code_navigate":
		switch strings.ToLower(strings.TrimSpace(req.Capability)) {
		case "file_outline", "outline", "read_symbol", "inspect_symbol", "symbol_body":
			return true
		}

		return false
	case "cpp_file_outline", "cpp_read_symbol",
		"go_file_outline", "go_read_symbol",
		"python_file_outline", "python_read_symbol",
		"ts_file_outline", "ts_read_symbol",
		"rust_file_outline", "rust_read_symbol",
		"java_file_outline", "java_read_symbol":
		return true
	}

	return false
}

// precedingResultWasReadClass returns true if the most recent recorded
// tool result was itself a read-class call. We use this to distinguish
// "model just wrote, now reads to prep next write" (first read after
// a write — let through) from "model is stacking verification reads"
// (read after read — intercept).
func precedingResultWasReadClass(results []workspace.ToolResult) bool {
	if len(results) == 0 {
		return false
	}

	last := results[len(results)-1]

	return workspace.IsReadTool(strings.TrimSpace(last.Tool))
}

// requiredEditPathsContains checks whether the request's target path
// appears in the brief's required_edit_paths list (under a normalized
// comparison).
func requiredEditPathsContains(required []string, target string) bool {
	for _, raw := range required {
		if agentutil.NormalizeRunnerPath(raw) == target {
			return true
		}
	}

	return false
}

// verificationInterceptDirective renders the redirect message that
// replaces the tool result. The text is deliberately short and
// actionable: the model needs the next path name, the rule, and the
// escape hatch in one block.
func verificationInterceptDirective(tool, target string, pending []string) string {
	next := pending[0]
	header := fmt.Sprintf(
		"%s on %q suppressed: that path is already on the ✓ list of required_edit_paths and the planner runs builds — do not re-verify completed edits.",
		strings.TrimSpace(tool), target,
	)
	body := fmt.Sprintf("Next required path (✗ pending): %s. Write it with edit_file or apply_patch, or stop with <<QUESTION>>: naming the concrete blocker for that path.", next)

	if len(pending) > 1 {
		body += fmt.Sprintf(" %d required path(s) remain in total: %s.", len(pending), strings.Join(pending, ", "))
	}

	return header + " " + body
}
