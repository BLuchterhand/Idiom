package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"path/filepath"
	"strings"
)

type deterministicEmptyReplyRecoveryStep struct {
	recover  func([]workspace.ToolResult) (workspace.ToolRequest, string, bool)
	runEvent func(workspace.ToolRequest) RunEvent
}

func (r *Runner) deterministicEmptyReplyRecovery(results []workspace.ToolResult) (workspace.ToolRequest, string, RunEvent, bool) {
	for _, step := range r.deterministicEmptyReplyRecoverySteps() {
		req, raw, ok := step.recover(results)
		if !ok {
			continue
		}

		return req, raw, step.runEvent(req), true
	}

	return workspace.ToolRequest{}, "", RunEvent{}, false
}

func (r *Runner) deterministicEmptyReplyRecoverySteps() []deterministicEmptyReplyRecoveryStep {
	return []deterministicEmptyReplyRecoveryStep{
		{
			recover: r.deterministicPlanExecuteRequiredSourceRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery found unread required edit path %s; issuing source read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicImplementationRequiredPathRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery found unread required edit path %s; issuing bounded read before another implementation attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicImplementationEvidencePathRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_evidence_read", Content: fmt.Sprintf("completion recovery found single implementation evidence path %s; issuing bounded read before another implementation attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryTargetRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_target_read", Content: fmt.Sprintf("completion recovery found candidate target %s in the runtime brief; issuing bounded read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoverySearch,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_search", Content: fmt.Sprintf("completion recovery found only context-packet candidates; issuing bounded literal search %q", req.Pattern)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryUnreadCandidateRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_read", Content: fmt.Sprintf("completion recovery found unread higher-confidence candidate path %s; issuing bounded read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_read", Content: fmt.Sprintf("completion recovery found candidate path %s after literal search; issuing bounded read before another final-answer attempt", req.Path)}
			},
		},
	}
}

func (r *Runner) noWriteRecoveryAllowed(results []workspace.ToolResult) bool {
	return r != nil &&
		r.unscopedNoWriteCompletionRecoveryApplies(results) &&
		!r.unscopedImplementationWriteReadyAfterNoWriteGate(results)
}

func noWriteRecoveryReadRequest(path, namedGap, whyNotAnswering string) (workspace.ToolRequest, string, bool) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        namedGap,
		WhyNotAnswering: whyNotAnswering,
	}

	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicNoWriteRecoveryRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != workspace.ToolNameSearchContent {
		return workspace.ToolRequest{}, "", false
	}

	paths := candidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
	if len(paths) == 0 || alreadyReadPath(results, paths[0]) {
		return workspace.ToolRequest{}, "", false
	}

	return noWriteRecoveryReadRequest(
		paths[0],
		"exact current code needed before writing the requested implementation change",
		"need current source before the write",
	)
}

func (r *Runner) unscopedImplementationWriteReadyAfterNoWriteGate(results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) || r.planThenExecutePlannerPhase() {
		return false
	}

	return implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results)
}

func successfulToolSeen(results []workspace.ToolResult, tool string) bool {
	tool = strings.TrimSpace(tool)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == tool && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func completionGateResultSeen(results []workspace.ToolResult) bool {
	return completionGateResultCount(results) > 0
}

func completionGateResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			count++
		}
	}

	return count
}

func (r *Runner) planThenExecuteSubmitReadyTerminalPrompt(errorText string, results []workspace.ToolResult, native bool) string {
	if r != nil && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		parts := []string{
			strings.TrimSpace(errorText),
			"The previous read/search request was rejected because plan-execute submit-correction mode is already ready. Do not read, search, inspect, verify, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or describe the plan in prose. The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence(),
			planThenExecuteTerminalSubmitPlanSkeleton(r.RuntimeBrief, results, native),
			"Fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders and do not request another tool.",
		}

		return strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n")
	}

	parts := []string{
		strings.TrimSpace(errorText),
		"The previous read/search request was rejected because plan-execute planner source evidence is already ready. Do not read, search, inspect, or verify again. The next visible action must be exactly one terminal outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing the current source path(s), or `<<QUESTION>>:` naming the exact blocker. Do not report this recovery rejection itself as the blocker.",
	}
	if r != nil {
		parts = append(parts, planThenExecuteTerminalSubmitPlanSkeleton(r.RuntimeBrief, results, native))
	}

	parts = append(parts, "If submitting a plan, fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.")

	return strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n")
}

func nonEmptyRunnerPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func lastNonGateTool(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == "completion_gate" {
			continue
		}

		if strings.TrimSpace(results[i].Error) != "" {
			continue
		}

		return tool
	}

	return ""
}

func lastNonGateAttemptWasUnavailableContextPack(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == "completion_gate" {
			continue
		}

		if tool != workspace.ToolNameContextPack {
			return false
		}

		return strings.TrimSpace(results[i].Error) != "" || !contextPackResultHasTargets(results[i])
	}

	return false
}

func alreadyReadPath(results []workspace.ToolResult, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
			return true
		}
	}

	return false
}
