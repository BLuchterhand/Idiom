package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"strings"
	"idiom/internal/runtime/agent/agentutil"
	"slices"
)

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !slices.Contains(currentAllowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	if summary, _, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, results, currentAllowedTools); !reject || summary != "Write rejected before source-body evidence" {
		return workspace.ToolRequest{}, "", false
	}

	paths := writeRequestPaths(req)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	target := agentutil.NormalizeRunnerPath(paths[0])
	if target == "" || concreteSourceEvidenceReadForPath(target, results) {
		return workspace.ToolRequest{}, "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            target,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence before applying the requested write",
		WhyNotAnswering: "need current source evidence for the model-selected write target before editing",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

type implementationDeferredSourceEvidenceWrite struct {
	Request         workspace.ToolRequest
	Raw             string
	EvidencePath    string
	RequiresHistory bool
}

func newImplementationDeferredSourceEvidenceWrite(req workspace.ToolRequest, raw string, evidencePath string) *implementationDeferredSourceEvidenceWrite {
	target := agentutil.NormalizeRunnerPath(evidencePath)
	if target == "" {
		paths := writeRequestPaths(req)
		if len(paths) == 1 {
			target = agentutil.NormalizeRunnerPath(paths[0])
		}
	}

	if target == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return nil
	}

	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(req)
	}

	return &implementationDeferredSourceEvidenceWrite{
		Request:      req,
		Raw:          raw,
		EvidencePath: target,
	}
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterEvidence(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if d == nil || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, d.RequiresHistory) {
		return workspace.ToolRequest{}, "", false
	}

	if d.RequiresHistory && !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !d.replayAllowedForRequest(req) {
		return workspace.ToolRequest{}, "", false
	}

	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}

	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterHistoryEvidence(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if d == nil || !d.RequiresHistory || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, true) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}

	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) fallbackVerificationRequest() (workspace.ToolRequest, bool) {
	if d == nil {
		return workspace.ToolRequest{}, false
	}

	paths := writeRequestPaths(d.Request)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, false
	}

	path := agentutil.NormalizeRunnerPath(paths[0])
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool: workspace.ToolNameSourceSanity,
		Path: path,
	}, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAllowedForRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	if d.RequiresHistory && tool == workspace.ToolNameShell {
		return true
	}

	switch tool {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability, workspace.ToolNameSourceSanity, workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol:
	default:
		return false
	}

	target := agentutil.NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		target = agentutil.NormalizeRunnerPath(req.Path)
	}

	return target == d.EvidencePath
}

func implementationDeferredWriteHasHistoryEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !successfulGitHistoryResultForPath(path, results) {
			return false
		}
	}

	return true
}

func implementationDeferredWriteCoveredBySourceEvidence(req workspace.ToolRequest, path string, results []workspace.ToolResult, allowAnchorMismatch bool) bool {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile:
		oldText := strings.TrimSpace(req.OldText)
		if oldText == "" {
			return false
		}

		if allowAnchorMismatch {
			return concreteSourceEvidenceReadForPath(path, results)
		}

		return implementationSourceEvidenceTextContains(path, results, oldText)
	case workspace.ToolNameWriteFile:
		return fullSourceReadFileSeen(path, results)
	case workspace.ToolNameAppendFile:
		return fullSourceReadFileSeen(path, results)
	default:
		return false
	}
}

func implementationSourceEvidenceTextContains(path string, results []workspace.ToolResult, want string) bool {
	path = agentutil.NormalizeRunnerPath(path)
	want = strings.TrimSpace(want)

	if path == "" || want == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !concreteSourceEvidenceResultMatchesPath(result, path) {
			continue
		}

		if strings.Contains(strings.Join([]string{result.Summary, result.Output}, "\n"), want) {
			return true
		}
	}

	return false
}

func planThenExecuteCandidateSourceReadyRejectionSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteCandidateSourceReadyResult(result) && strings.TrimSpace(result.Error) != "" {
			return true
		}
	}

	return false
}

func planThenExecuteCandidateSourceReadyResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Code) == planThenExecuteResultCandidateSourceReady {
		return true
	}

	// Compatibility for already-recorded logs and older seeded tests while
	// new behavior writes and reads the typed result code above.
	return strings.TrimSpace(result.Code) == "" &&
		strings.TrimSpace(result.Summary) == "Candidate source already ready"
}

func currentToolSurfaceRejection(req workspace.ToolRequest, allowedTools []string) (string, string, bool) {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || slices.Contains(allowedTools, tool) {
		return "", "", false
	}

	if len(allowedTools) == 0 {
		return "Tool not available in current runtime contract", fmt.Sprintf("%s is not available because the current runtime contract exposes no tools. Answer from gathered evidence or report a concrete blocker.", tool), true
	}

	return "Tool not available in current runtime contract", fmt.Sprintf("%s is not in the current allowed tool surface. Available tools: %s. Use an available tool, answer from gathered evidence, or report a concrete blocker.", tool, strings.Join(allowedTools, ", ")), true
}

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRejection(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (string, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          results,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, r.RuntimeBrief.AllowedTools),
	})
	if contract.PhaseID != turnPhaseImplementationTargetSource && contract.Phase != "target_write_ready" {
		return "", "", false
	}

	if strings.TrimSpace(req.Tool) == workspace.ToolNameWriteFile && strings.TrimSpace(req.Content) != "" && r.writeRequestTargetsAllMissing(req) {
		return "", "", false
	}

	if implementationTargetSourceWriteTargetsHaveEvidence(req, results) {
		return "", "", false
	}

	return "Write rejected before source-body evidence",
		"implementation.target_source requires target localization and source-body evidence before changing existing source. Use a bounded read/discovery tool with named_gap for the missing target or edit-safety fact, use write_file without prior source-body evidence only for a genuinely new file, or answer <<QUESTION>> with the exact blocker/no-op evidence.",
		true
}

func (r *Runner) writeRequestTargetsAllMissing(req workspace.ToolRequest) bool {
	if r == nil || r.Workspace == nil {
		return false
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		clean := cleanRelativeRunnerPath(path)
		if clean == "" {
			return false
		}

		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean)))
		if err == nil || !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func implementationTargetSourceWriteTargetsHaveEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		path = agentutil.NormalizeRunnerPath(path)
		if path == "" || !concreteSourceEvidenceReadForPath(path, results) {
			return false
		}
	}

	return true
}

func runnerHasWorkspaceWriteCapability(brief workerruntime.WorkerRuntimeBrief, tools []string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "write") {
		return true
	}

	for _, tool := range tools {
		if workspace.IsWorkspaceWriteTool(tool) {
			return true
		}
	}

	return false
}

func runnerVerificationRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.ToLower(strings.TrimSpace(brief.VerificationContract))
	return contract != "" && !strings.Contains(contract, "not_required")
}

func defaultRunnerStateSlice(values []string, defaults []string) []string {
	if len(values) > 0 {
		return values
	}

	return append([]string(nil), defaults...)
}

func appendRunnerTools(values []string, tools ...string) []string {
	for _, tool := range tools {
		values = util.AppendUnique(values, tool)
	}

	return values
}

func filterOutRunnerTools(values []string, disallowed ...string) []string {
	if len(values) == 0 || len(disallowed) == 0 {
		return values
	}

	blocked := make(map[string]struct{}, len(disallowed))
	for _, tool := range disallowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			blocked[tool] = struct{}{}
		}
	}

	filtered := values[:0]
	for _, tool := range values {
		if _, skip := blocked[strings.TrimSpace(tool)]; skip {
			continue
		}

		filtered = append(filtered, tool)
	}

	return filtered
}
