package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"slices"
	"strings"
)

func (r *Runner) initialBootstrapInstruction() string {
	if r == nil {
		return ""
	}

	family := strings.TrimSpace(r.RuntimeBrief.TaskFamily)
	profile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile)

	switch {
	case strings.HasPrefix(r.agentName(), "child:") && family == "delegated_read_only":
		return strings.Join([]string{
			"[delegated read-only bootstrap]",
			"Consume the delegated scope first. Do not start with directory walking when the delegated prompt already names paths or components.",
			"Use one exact-text or language-symbol capability to locate the mechanism, then inspect the best hit directly with a bounded read or symbol-read capability.",
			"For broad architecture or boundary questions, prefer a composite discovery_query first, then inspect only the named paths or symbols that answer the delegated question.",
			"After any successful search hit, inspect that hit before doing another filename or content search.",
			"Stop as soon as the delegated question is answered. Return concise grounded findings with inspected paths and confidence.",
		}, "\n")
	case workerruntime.TaskFamilyIsArchitecture(family) && workerruntime.RuntimeContractIsArchitecture(r.RuntimeBrief.Contract):
		return strings.Join([]string{
			"[architecture bootstrap]",
			"Start broad architecture questions with delegated read-only discovery when the task tool is available.",
			"The delegated packet should establish inspected paths, boundary evidence, confidence, and named gaps before local follow-up.",
			"After a child packet, answer unless one exact missing claim needs one bounded probe.",
		}, "\n")
	case workerruntime.TaskFamilyIsVerificationOnly(family) || workerruntime.PermissionProfileIsReadOnlyVerificationShell(profile):
		cmd := "go test ./..."
		if len(r.RuntimeBrief.VerificationPlan) > 0 && strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0])
		}

		return strings.Join([]string{
			"[verification bootstrap]",
			"Verification task contract: run the configured verification command before inspection.",
			fmt.Sprintf("Configured verification command: %s", cmd),
			"After it runs, answer with the observed pass/fail result.",
		}, "\n")
	case family == "debug_logs":
		return strings.Join([]string{
			"[debug-log bootstrap]",
			"Start from the newest relevant log artifact; source-code orientation is lower value until log evidence is exhausted.",
			"First choose the newest real log file (for example under logs/, *.log, or *.jsonl), then read its recent tail.",
			"Stay on that selected log until you can cite the weird decision or declare the missing datum.",
			"After one bounded log read, answer from the logs if the cause is already clear; otherwise do one targeted follow-up inside the same log.",
		}, "\n")
	case isEvidenceFirstMutationTaskFamily(family) && len(r.RuntimeBrief.TargetRefs) > 0:
		return strings.Join([]string{
			"[evidence-first mutation bootstrap]",
			fmt.Sprintf("This turn will update named target file(s): %s.", strings.Join(r.RuntimeBrief.TargetRefs, ", ")),
			"First gather source evidence from the implementation files or configs that drive the change.",
			"Then read only the target section you plan to edit.",
		}, "\n")
	default:
		return ""
	}
}

func (r *Runner) verificationBootstrapRequest() (workspace.ToolRequest, bool) {
	if r == nil {
		return workspace.ToolRequest{}, false
	}

	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) {
		return workspace.ToolRequest{}, false
	}

	cmd := "go test ./..."

	for _, candidate := range r.RuntimeBrief.VerificationPlan {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			cmd = trimmed
			break
		}
	}

	if strings.TrimSpace(cmd) == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:    workspace.ToolNameShell,
		Command: cmd,
		Workdir: ".",
	}, true
}

func synthesizeVerificationOnlyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(req.Tool)
	}

	status := "passed"
	if strings.TrimSpace(result.Error) != "" {
		status = "failed"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Verification %s.\n\n", status)

	if command != "" {
		fmt.Fprintf(&b, "Command: `%s`\n", command)
	}

	if errText := strings.TrimSpace(result.Error); errText != "" {
		fmt.Fprintf(&b, "Failure: %s\n", errText)
	}

	if output := compactOutput(result.Output, 24); strings.TrimSpace(output) != "" {
		b.WriteString("\nOutput:\n")
		b.WriteString(output)
		b.WriteString("\n")
	}

	b.WriteString("\nNo files were edited.")

	return strings.TrimSpace(b.String())
}

func (r *Runner) debugLogPreflightRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !debugLogContractApplies(r.RuntimeBrief, results) || debugLogEvidenceSatisfied(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}

	discovery, _ := debugLogDiscoveryRequest(r.Workspace)
	selection, ok := latestStableDebugLogSelection(results, currentDebugLogSessionID(r.Workspace))

	if !ok {
		if debugLogRequestMatchesDiscovery(req, discovery) || isConcreteDebugLogArtifactRequest(req) {
			return workspace.ToolRequest{}, false
		}

		return discovery, true
	}

	if !hasDebugLogReadForSelection(results, selection) {
		if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && debugLogRequestTargetsSelection(req, selection) {
			return workspace.ToolRequest{}, false
		}

		return debugLogTailRequest(selection), true
	}

	if _, ok := latestDebugLogSearchHit(results); !ok {
		if shouldDeepenDebugLogSearch(results, selection) {
			if next, ok := nextStableDebugLogSelection(results, selection, currentDebugLogSessionID(r.Workspace)); ok {
				if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && debugLogRequestTargetsSelection(req, next) {
					return workspace.ToolRequest{}, false
				}

				return debugLogTailRequest(next), true
			}
		}

		if redirected, ok := debugLogScopedSearchRedirect(req, selection); ok {
			return redirected, true
		}
	}

	return workspace.ToolRequest{}, false
}

func prematureTargetReadRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", false
	}

	path := strings.TrimSpace(req.Path)
	if path == "" || !slices.Contains(brief.TargetRefs, path) {
		return "", "", false
	}

	if !isLargeTargetRead(req) {
		return "", "", false
	}

	if hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "", "", false
	}

	return "Premature target-file read rejected",
		"this turn needs source evidence before editing the named target file. Inspect the implementation files or configs that drive the change, then read only the target section you plan to edit.",
		true
}

func evidenceFirstTargetReadRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return workspace.ToolRequest{}, false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, false
	}

	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return workspace.ToolRequest{}, false
	}

	if targetReadCount == 0 {
		if !isReadClassTool(req) {
			return workspace.ToolRequest{}, false
		}

		path := strings.TrimSpace(req.Path)
		if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && slices.Contains(brief.TargetRefs, path) && !isLargeTargetRead(req) {
			return workspace.ToolRequest{}, false
		}

		return firstTargetReadWindowRequest(brief.TargetRefs), true
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !slices.Contains(brief.TargetRefs, strings.TrimSpace(req.Path)) {
		return workspace.ToolRequest{}, false
	}

	if !targetReadNeedsBounding(req) {
		return workspace.ToolRequest{}, false
	}

	return boundedFollowupTargetReadRequest(req, results, brief.TargetRefs), true
}

func isEvidenceFirstMutationTaskFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return true
	default:
		return false
	}
}

func isLargeTargetRead(req workspace.ToolRequest) bool {
	start := req.StartLine
	end := req.EndLine

	if start <= 0 && end <= 0 {
		return true
	}

	if start <= 0 {
		start = 1
	}

	if end <= 0 {
		return true
	}

	return end-start > 160
}

func hasFocusedNonTargetEvidence(results []workspace.ToolResult, targetRefs []string) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		switch tool := strings.TrimSpace(result.Tool); {
		case tool == workspace.ToolNameReadFile:
			path := extractRunnerFilePath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		case strings.HasSuffix(tool, "_file_outline"), strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			path := extractSemanticInspectionPath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		}
	}

	return false
}

func hasSuccessfulTargetRead(results []workspace.ToolResult, targetRefs []string) bool {
	return successfulTargetReadCount(results, targetRefs) > 0
}

func successfulTargetReadCount(results []workspace.ToolResult, targetRefs []string) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if path := extractRunnerFilePath(result.Summary); slices.Contains(targetRefs, path) {
			count++
		}
	}

	return count
}

func evidenceFirstTargetReadUnlocked(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		!hasSuccessfulTargetRead(results, brief.TargetRefs)
}

const (
	initialEvidenceFirstTargetReadWindowEnd = 220
	followupEvidenceFirstTargetReadLines    = 140
)

func firstTargetReadWindowRequest(targetRefs []string) workspace.ToolRequest {
	for _, path := range targetRefs {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			return workspace.ToolRequest{
				Tool:      workspace.ToolNameReadFile,
				Path:      trimmed,
				StartLine: 1,
				EndLine:   initialEvidenceFirstTargetReadWindowEnd,
			}
		}
	}

	return workspace.ToolRequest{}
}

func boundedFollowupTargetReadRequest(req workspace.ToolRequest, results []workspace.ToolResult, targetRefs []string) workspace.ToolRequest {
	start := req.StartLine
	if start <= 0 {
		if window, ok := latestSuccessfulTargetReadWindow(results, targetRefs); ok {
			start = window.End + 1
		} else {
			start = 1
		}
	}

	if start <= 0 {
		start = 1
	}

	return workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      strings.TrimSpace(req.Path),
		StartLine: start,
		EndLine:   start + followupEvidenceFirstTargetReadLines - 1,
	}
}

func targetReadNeedsBounding(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 {
		return true
	}

	return end-start+1 > followupEvidenceFirstTargetReadLines
}

func latestSuccessfulTargetReadWindow(results []workspace.ToolResult, targetRefs []string) (debugLogReadWindow, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok || !slices.Contains(targetRefs, strings.TrimSpace(window.Path)) {
			continue
		}

		return window, true
	}

	return debugLogReadWindow{}, false
}

func clearEvidenceFirstTargetReadActionClasses(rejectedActions map[string]int, brief workerruntime.WorkerRuntimeBrief) {
	if len(rejectedActions) == 0 {
		return
	}

	for _, path := range brief.TargetRefs {
		actionClass := toolActionClass(workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: strings.TrimSpace(path)})
		if actionClass == "" {
			continue
		}

		delete(rejectedActions, actionClass)
	}
}

func clearRejectedFingerprintSummary(rejected map[string]string, fingerprint string, summary string) {
	fingerprint = strings.TrimSpace(fingerprint)
	summary = strings.TrimSpace(summary)

	if fingerprint == "" || summary == "" || len(rejected) == 0 {
		return
	}

	if strings.TrimSpace(rejected[fingerprint]) == summary {
		delete(rejected, fingerprint)
	}
}
