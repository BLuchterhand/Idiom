package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/pathparse"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type debugLogSearchHit struct {
	Handle string
	Path   string
	Line   int
}

type debugLogFileSelection struct {
	Handle string
	Path   string
}

type debugLogReadWindow struct {
	Path        string
	CanonicalID string
	Start       int
	End         int
}

func (window debugLogReadWindow) MatchesPath(path string) bool {
	path = normalizedDiagnosticLogEvidenceReference(path)
	return path != "" && (path == window.Path || path == window.CanonicalID)
}

var singleSearchContentSummaryRE = regexp.MustCompile(`^Found 1 matching line at (.+):([0-9]+)\.`)

func debugLogEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !debugLogContractApplies(brief, results) {
		return false
	}

	hit, ok := latestDebugLogSearchHit(results)
	if !ok {
		return false
	}

	return hasDebugLogReadWindowForHit(results, hit)
}

func latestDebugLogSelection(results []workspace.ToolResult) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !mentionsConcreteDebugLogSelector(window.Path) {
				continue
			}

			return debugLogFileSelection{Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !mentionsConcreteDebugLogSelector(hit.Path) {
				continue
			}

			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case workspace.ToolNameSearchFiles:
			selection, ok := parseDebugLogFileSelection(result)
			if ok {
				return selection, true
			}
		}
	}

	return debugLogFileSelection{}, false
}

func parseDebugLogFileSelection(result workspace.ToolResult) (debugLogFileSelection, bool) {
	selections := parseDebugLogFileSelections(result)
	if len(selections) == 0 {
		return debugLogFileSelection{}, false
	}

	return selections[0], true
}

func parseDebugLogFileSelections(result workspace.ToolResult) []debugLogFileSelection {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchFiles || strings.TrimSpace(result.Error) != "" {
		return nil
	}

	var selections []debugLogFileSelection

	for _, line := range strings.Split(result.Output, "\n") {
		handle, path := parseSearchFilesSelectionLine(line)
		if path == "" || !mentionsConcreteDebugLogSelector(path) {
			continue
		}

		selections = append(selections, debugLogFileSelection{Handle: handle, Path: path})
	}

	return selections
}

func latestStableDebugLogSelection(results []workspace.ToolResult, sessionID string) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !mentionsConcreteDebugLogSelector(window.Path) || currentSessionDebugLogPath(window.Path, sessionID) {
				continue
			}

			return debugLogFileSelection{Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !mentionsConcreteDebugLogSelector(hit.Path) || currentSessionDebugLogPath(hit.Path, sessionID) {
				continue
			}

			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case workspace.ToolNameSearchFiles:
			for _, selection := range parseDebugLogFileSelections(result) {
				if currentSessionDebugLogPath(selection.Path, sessionID) {
					continue
				}

				return selection, true
			}
		}
	}

	return debugLogFileSelection{}, false
}

func parseSearchFilesSelectionLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}

	handle := ""
	if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "f") {
		handle = strings.TrimSpace(fields[0])
		line = strings.TrimSpace(strings.TrimPrefix(line, handle))
	}

	if idx := strings.Index(line, " (modified "); idx >= 0 {
		line = line[:idx]
	}

	return handle, strings.TrimSpace(line)
}

func hasDebugLogReadForSelection(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		if selection.Path != "" && window.MatchesPath(selection.Path) {
			return true
		}
	}

	return false
}

func nextDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection) (debugLogFileSelection, bool) {
	return nextStableDebugLogSelection(results, current, "")
}

func nextStableDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection, sessionID string) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchFiles || strings.TrimSpace(result.Error) != "" {
			continue
		}

		selections := parseDebugLogFileSelections(result)
		for idx := range selections {
			selection := selections[idx]
			if !sameDebugLogSelection(selection, current) {
				continue
			}

			for nextIdx := idx + 1; nextIdx < len(selections); nextIdx++ {
				if currentSessionDebugLogPath(selections[nextIdx].Path, sessionID) {
					continue
				}

				if !hasDebugLogReadForSelection(results, selections[nextIdx]) {
					return selections[nextIdx], true
				}
			}
		}
	}

	return debugLogFileSelection{}, false
}

func debugLogContractApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) &&
		!workerruntime.RuntimeContractIsArchitecture(brief.Contract) &&
		!workerruntime.RuntimeContractIsReviewFindings(brief.Contract) {

		return false
	}

	if !objectiveRequestsDiagnosticLogInspection(brief.Objective) {
		return false
	}

	if objectiveTreatsDiagnosticLogsAsOptionalEvidence(brief.Objective) {
		return false
	}

	return true
}

func objectiveRequestsDiagnosticLogInspection(objective string) bool {
	words := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	if strings.TrimSpace(words) == "" {
		return false
	}

	if strings.Contains(words, " read these files") || strings.Contains(words, " read the files") {
		return false
	}

	if objectiveRequestsSourceFeatureInspection(words) && !objectiveNamesConcreteDiagnosticLogArtifact(words) {
		return false
	}

	hasDiagnosticArtifact := false

	for _, artifact := range []string{
		" log", "logs", "telemetry log", "telemetry/", "conversation log", "conversations/",
		"conversation/log", "session log", "debug log", "debuglog", ".jsonl", ".log",
	} {
		if strings.Contains(words, artifact) {
			hasDiagnosticArtifact = true
			break
		}
	}

	if !hasDiagnosticArtifact {
		return false
	}

	for _, intent := range []string{
		" inspect ", " investigate ", " review ", " analyze ", " analyse ", " check ",
		" identify ", " diagnose ", " look at ", " open ", " read ", " tail ", " scan ", " audit ",
		" actual conversation logs", " newest log", " recent log", " latest log",
		" previous log", " prior log", " conversation logs", " telemetry logs",
	} {
		if strings.Contains(words, intent) {
			return true
		}
	}

	hasIncidentQuestion := strings.Contains(words, " why did ") ||
		strings.Contains(words, " why was ") ||
		strings.Contains(words, " what happened") ||
		strings.Contains(words, " did it actually") ||
		strings.Contains(words, " hit objective")
	if !hasIncidentQuestion {
		return false
	}

	for _, failureMarker := range []string{" failure", " failed", " error", " reject", " rejected", " blocker", " unmet", " overreach", " bad answer"} {
		if strings.Contains(words, failureMarker) {
			return true
		}
	}

	return false
}

func objectiveTreatsDiagnosticLogsAsOptionalEvidence(objective string) bool {
	words := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	if strings.TrimSpace(words) == "" {
		return false
	}

	if !ContainsAny(words, " if available", " if present", " when available", " where available") {
		return false
	}

	return ContainsAny(words,
		" source ", " source files ", " code ", " bug ", " behavior bug ", " rca ",
		" tests ", " fix shape ", " owning boundary ", " implementation ",
	)
}

func objectiveRequestsSourceFeatureInspection(words string) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}

	return ContainsAny(words,
		" codebase",
		" source ",
		" source-code",
		" go file",
		" go files",
		" config",
		" configuration",
		" flag ",
		" flag controls",
		" defined",
		" definition",
		" implementation",
		" logic",
		" mode ",
		" when enabled",
		" field",
		" fields",
		" value",
		" values",
		" type",
		" types",
		" struct",
		" structs",
	)
}

func objectiveNamesConcreteDiagnosticLogArtifact(words string) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}

	return ContainsAny(words,
		" actual conversation logs",
		" newest log",
		" recent log",
		" latest log",
		" previous log",
		" prior log",
		" conversation logs",
		" telemetry logs",
		" log file",
		" log artifact",
		" logs/",
		" telemetry/",
		" conversations/",
		" .jsonl",
		" .log",
	)
}

func currentDebugLogSessionID(mgr *workspace.Manager) string {
	if mgr == nil {
		return ""
	}

	return strings.TrimSpace(mgr.SessionID())
}

func currentSessionDebugLogPath(path string, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	clean := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
	if !mentionsConcreteDebugLogSelector(clean) {
		return false
	}

	base := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))

	return base == sessionID || strings.HasPrefix(base, sessionID+"-")
}

func sameDebugLogSelection(a debugLogFileSelection, b debugLogFileSelection) bool {
	if a.Handle != "" && b.Handle != "" && a.Handle == b.Handle {
		return true
	}

	return a.Path != "" && b.Path != "" && a.Path == b.Path
}

func shouldDeepenDebugLogSearch(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	_ = selection

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return false
		}

		return workerruntime.IsZeroHitSearchResult(result)
	}

	return false
}

func debugLogRequestTargetsSelection(req workspace.ToolRequest, selection debugLogFileSelection) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		if selection.Handle != "" && textutil.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Handle)) == selection.Handle {
			return true
		}

		return selection.Path != "" && strings.TrimSpace(req.Path) == selection.Path
	case workspace.ToolNameSearchContent:
		if selection.Path == "" {
			return false
		}

		return strings.TrimSpace(req.Path) == selection.Path || strings.TrimSpace(req.Glob) == selection.Path
	default:
		return false
	}
}

func debugLogSelectionMatchesPath(selection debugLogFileSelection, path string) bool {
	return NormalizeRunnerPath(selection.Path) == NormalizeRunnerPath(path)
}

func concreteDebugLogArtifactPathForRequest(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	for _, value := range []string{req.Path, req.Glob} {
		if mentionsConcreteDebugLogSelector(value) {
			return strings.TrimSpace(value), true
		}
	}

	handle := textutil.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Handle))
	if handle == "" {
		return "", false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]

		if hit, ok := parseDebugLogSearchHit(result); ok && hit.Handle == handle && mentionsConcreteDebugLogSelector(hit.Path) {
			return hit.Path, true
		}

		for _, selection := range parseDebugLogFileSelections(result) {
			if selection.Handle == handle && mentionsConcreteDebugLogSelector(selection.Path) {
				return selection.Path, true
			}
		}
	}

	return "", false
}

func debugLogReadRequestAddsWindow(req workspace.ToolRequest, results []workspace.ToolResult, selection debugLogFileSelection) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !debugLogRequestTargetsSelection(req, selection) {
		return false
	}

	if req.StartLine <= 0 && req.EndLine <= 0 {
		return false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]

		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		pathMatches := selection.Path != "" && window.MatchesPath(selection.Path)
		if !pathMatches {
			continue
		}

		return !debugLogRequestWindowCovered(req, window)
	}

	return true
}

func debugLogRequestWindowCovered(req workspace.ToolRequest, window debugLogReadWindow) bool {
	start := req.StartLine
	end := req.EndLine

	if start <= 0 {
		start = 1
	}

	if end <= 0 {
		return false
	}

	return window.Start <= start && window.End >= end
}

func debugLogRequestMatchesDiscovery(req workspace.ToolRequest, discovery workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != strings.TrimSpace(discovery.Tool) {
		return false
	}

	return strings.TrimSpace(req.Pattern) == strings.TrimSpace(discovery.Pattern) &&
		strings.TrimSpace(req.Path) == strings.TrimSpace(discovery.Path)
}

func isConcreteDebugLogArtifactRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		return mentionsConcreteDebugLogSelector(strings.TrimSpace(req.Path))
	case workspace.ToolNameSearchContent:
		return mentionsConcreteDebugLogSelector(strings.TrimSpace(req.Path)) || mentionsConcreteDebugLogSelector(strings.TrimSpace(req.Glob))
	default:
		return false
	}
}

func mentionsConcreteDebugLogSelector(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.Contains(lower, "/log/") ||
		strings.HasPrefix(lower, "log/") ||
		strings.Contains(lower, "/traces/") ||
		strings.HasPrefix(lower, "traces/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func exactDebugLogFilePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" || strings.ContainsAny(lower, "*?[") {
		return false
	}

	return strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func isGeneratedDiscoveryContextArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))

	lower = strings.TrimPrefix(lower, "./")

	return strings.HasPrefix(lower, ".idiom/discovery/queries/") ||
		strings.HasPrefix(lower, ".idiom/comprehension/views/") ||
		strings.HasPrefix(lower, ".codex/discovery/")
}

func latestDebugLogSearchHit(results []workspace.ToolResult) (debugLogSearchHit, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if hit, ok := parseDebugLogSearchHit(result); ok {
			return hit, true
		}
	}

	return debugLogSearchHit{}, false
}

func parseDebugLogSearchHit(result workspace.ToolResult) (debugLogSearchHit, bool) {
	match := singleSearchContentSummaryRE.FindStringSubmatch(strings.TrimSpace(result.Summary))
	if len(match) != 3 {
		return debugLogSearchHit{}, false
	}

	line := parseRunnerInt(match[2])
	if line <= 0 {
		return debugLogSearchHit{}, false
	}

	hit := debugLogSearchHit{Path: strings.TrimSpace(match[1]), Line: line}
	firstLine := firstNonEmptyLine(result.Output)

	if firstLine == "" {
		return hit, true
	}

	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "f") {
		hit.Handle = strings.TrimSpace(parts[0])
	}

	return hit, true
}

func hasDebugLogReadWindowForHit(results []workspace.ToolResult, hit debugLogSearchHit) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		if hit.Path != "" && window.MatchesPath(hit.Path) && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
	}

	return false
}

func debugLogRequestTargetsHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Handle != "" && strings.TrimSpace(req.ID) == hit.Handle {
		return true
	}

	return hit.Path != "" && strings.TrimSpace(req.Path) == hit.Path
}

func debugLogRequestCoversHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Line <= 0 {
		return false
	}

	start := req.StartLine
	end := req.EndLine

	if start <= 0 {
		start = hit.Line
	}

	if end <= 0 {
		end = start + 80
	}

	return hit.Line >= start && hit.Line <= end
}

func parseDebugLogReadWindow(result workspace.ToolResult) (debugLogReadWindow, bool) {
	for _, fact := range newToolResultEvidenceRecord(result).diagnosticLogReads {
		return debugLogReadWindow{
			Path:        fact.PolicySubject,
			CanonicalID: fact.CanonicalID,
			Start:       fact.StartLine,
			End:         fact.EndLine,
		}, true
	}

	return debugLogReadWindow{}, false
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}

	return ""
}

func parseRunnerInt(s string) int {
	value := 0

	for _, ch := range strings.TrimSpace(s) {
		if ch < '0' || ch > '9' {
			return 0
		}

		value = value*10 + int(ch-'0')
	}

	return value
}

func answerFirstNoToolsPrompt(reason string) string {
	reason = strings.TrimSpace(reason)

	contract := "Final answer contract: tools are unavailable now. Write the normal final answer from the evidence already gathered. Do not emit XML tool-call tags, tool JSON, or tool-request syntax of any kind — write only plain prose. If the evidence supports a concrete answer, answer directly and do not lead with budget, blocker, partial-findings, or recommended-next-step framing. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action.\n\nA good answer explains the mechanism and names the evidence: state what happens, which code paths are involved, and cite the files or functions you inspected. For example: 'Post-write validation runs through source_sanity (runner_verification.go:120-200) which checks changed files with go vet. The implement lane calls this directly after each write, while plan-execute defers verification to the review-repair phase.' Do not dump raw function signatures — synthesize what you learned."

	if reason == "" {
		return contract
	}

	if strings.Contains(reason, "Final answer contract:") {
		return reason
	}

	return strings.TrimSpace(reason + "\n\n" + contract)
}

type noToolsCloseoutMode string

const (
	noToolsCloseoutModelAllowed          noToolsCloseoutMode = "model_allowed"
	noToolsCloseoutDeterministicRequired noToolsCloseoutMode = "deterministic_required"
)

func (r *runExecution) completeWithoutMoreToolsWithReasonMode(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
	metrics *runnerMetrics,
	mode noToolsCloseoutMode,
) (string, error) {

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Tools are now disabled."
	}

	reason = answerFirstNoToolsPrompt(reason)

	if mode == noToolsCloseoutDeterministicRequired {
		return r.terminalStatusCloseoutAnswer(reason, collectedResults), nil
	}

	preferModelSynthesis := shouldPreferModelBudgetSynthesis(r.agentName(), r.state.runtimeBrief, collectedResults)
	statusProjection := r.terminalStatusCloseoutAnswer(reason, collectedResults)
	reason = strings.TrimSpace(reason + "\n\nTyped closeout status (use as evidence, not as a response template):\n" + statusProjection)

	if preferModelSynthesis {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
		if paths := newToolResultEvidenceProjection(collectedResults).delegatedClosureEvidencePaths(r.state.runtimeBrief.Objective, 4); len(paths) > 0 {
			reason = strings.TrimSpace(reason + "\nCarry forward the inspected implementation paths explicitly in the answer: " + summarizePostChildEvidencePaths(paths, 4) + ". Do not collapse the answer to a single file when the evidence already spans multiple relevant implementation paths.")
		}
	}

	// Keep the model-facing closeout aligned with the run-owned ledger. Tool
	// output is a rendered view and must not become lifecycle authority.
	if hint := taskLedgerClosureHint(r.taskLedger); hint != "" {
		reason = hint + "\n\n" + reason
	}

	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.state.runtimeBrief)
	requestMessages := r.chatMessagesForOptions(turnMessages, textOnlyOpts)

	assistantMessage, err := r.runner.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "forced_completion", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, textOnlyOpts, false)

	deterministicFallback := func() (string, bool) {
		return statusProjection, strings.TrimSpace(statusProjection) != ""
	}

	if len(assistantMessage.ToolCalls) > 0 {
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "native tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	if IsTextualToolCall(reply) {
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "textual tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "malformed tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return "", parseErr
	} else if ok {
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
	if err != nil {
		return "", err
	}

	if _, rejected := r.completionGateRejection(answer, collectedResults, 0, r.taskLedger, false); rejected {
		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return statusProjection, nil
	}

	if _, rejected := r.readOnlyNonSubstantiveCompletionRejection(answer, collectedResults); rejected {
		if fallback, ok := deterministicFallback(); ok {
			return fallback, nil
		}

		return statusProjection, nil
	}

	if finalAnswerNeedsDecoration(answer) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, textOnlyOpts, answer+"\n\n"+statusProjection, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return repaired, nil
		}

		return statusProjection, nil
	}

	return answer, nil
}

func (r *runExecution) terminalStatusCloseoutAnswer(reason string, results []workspace.ToolResult) string {
	if r == nil {
		return "Closeout status: unavailable."
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) {
		state := r.implementationCompletionState(results)
		switch state.TerminalDisposition {
		case implementationTerminalDispositionPlanSubmitted:
			if state.CloseoutReady() {
				return "Deliverable status: plan_submitted. A successful submit_plan result is recorded; executor-side workspace mutation is outside this planner run."
			}
		case implementationTerminalDispositionMutationComplete:
			return r.synthesizeMutationFallbackAnswer(results)
		}

		if blocker, ok := r.implementationContractCloseoutBlocker(results, reason); ok {
			return blocker
		}
	}

	return r.budgetDeterministicBlocker(reason, results)
}

func (r *runExecution) repairInternalFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	internalAnswer string,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {

	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false, nil
	}

	repairMessages := append([]llama.ChatMessage(nil), turnMessages...)

	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: "The previous final synthesis still did not produce a usable user-facing answer. Tools remain disabled. " +
			"The text below is an internal evidence summary, not a final-answer template. Rewrite it into the normal final answer to the user's request using only claims supported by that evidence and the prior tool results. " +
			"Do not mention evidence packets, probes, tool calls, budgets, fallback behavior, or internal sections. If one exact claim is unsupported, state that uncertainty at the end.\n\nInternal evidence summary:\n" + internalAnswer,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	modelStart := time.Now()
	requestMessages := r.chatMessagesForOptions(repairMessages, opts)

	repairMessage, err := r.runner.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(repairMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "fallback_final_answer_retry", requestMessages, repairMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "fallback_final_answer_retry_reply", repairMessage, opts, false)

	if len(repairMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(repairMessage.Content)
	if content == "" || IsTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if finalAnswerNeedsDecoration(content) {
		return "", false, nil
	}

	// Run the completion gate on the repaired answer. The retry path
	// (retryInvalidFinalAnswer) already runs this gate, but this
	// fallback-repair path was skipping it, allowing garbage answers
	// like raw tool result transcripts to reach the user.
	if _, rejected := r.completionGateRejection(content, collectedResults, 0, r.taskLedger, false); rejected {
		return "", false, nil
	}

	r.state.lastAssistantMessage = repairMessage

	return content, true, nil
}

func (r *runExecution) retryInvalidFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
	reason string,
) (string, bool, error) {

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, r.assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool request"
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous final response was a " + reason + ", but tools are disabled for final synthesis. " +
			"Do not emit tool JSON, XML, tool blocks, or another probe. Reply in normal prose from the evidence already gathered. " +
			"If the evidence is sufficient, answer directly; if one exact claim remains unsupported, add that uncertainty after the supported answer in one sentence. " +
			"Do not list internal tool traces, exact probes, or inspected-path inventories unless they are necessary to explain the answer.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	modelStart := time.Now()
	requestMessages := r.chatMessagesForOptions(retryMessages, opts)

	retryMessage, err := r.runner.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "invalid_final_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "invalid_final_answer_retry_reply", retryMessage, opts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(retryMessage.Content)
	if content == "" || IsTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if _, rejected := r.completionGateRejection(content, collectedResults, 0, r.taskLedger, false); rejected {
		return r.terminalStatusCloseoutAnswer("The final-answer retry was rejected by the active completion contract.", collectedResults), true, nil
	}

	if finalAnswerNeedsDecoration(content) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, content, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			return repaired, true, nil
		}

		return r.terminalStatusCloseoutAnswer("The final-answer retry remained invalid after repair.", collectedResults), true, nil
	}

	r.state.lastAssistantMessage = retryMessage

	return content, true, nil
}

type noToolsFallbackPhase string

const (
	noToolsFallbackBeforeModel                 noToolsFallbackPhase = "before_model"
	noToolsFallbackAfterBudgetExhausted        noToolsFallbackPhase = "after_budget_exhausted"
	noToolsFallbackAfterInvalidModelCloseout   noToolsFallbackPhase = "after_invalid_model_closeout"
	noToolsFallbackAfterRejectedModelCloseout  noToolsFallbackPhase = "after_rejected_model_closeout"
	noToolsFallbackAfterEmptyAnswerRetryFailed noToolsFallbackPhase = "after_empty_answer_retry_failed"
)

func (r *runExecution) deterministicNoToolsFallback(phase noToolsFallbackPhase, results []workspace.ToolResult) (string, bool) {
	if r == nil || !r.shouldPreferDeterministicNoToolsAnswer(results) {
		return "", false
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) && phase == noToolsFallbackBeforeModel {
		return "", false
	}

	return r.terminalStatusCloseoutAnswer("Tools and model retries are exhausted ("+string(phase)+").", results), true
}

func (r *runExecution) shouldPreferDeterministicNoToolsAnswer(results []workspace.ToolResult) bool {
	if r == nil {
		return false
	}
	brief := r.state.runtimeBrief
	if runnerBriefRequiresMutation(brief) {
		return r.implementationCompletionState(results).HasChanges() ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0 ||
			targetPacketSupportsWrite(brief)
	}

	if runnerVerificationRequired(brief) {
		return false
	}

	return false
}

func presentableFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	if runnerBriefRequiresMutation(brief) {
		return answer, true
	}

	return readOnlyClosureDecisionForResults(brief, results).PresentableFallbackCandidate(agentName, answer)
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if strings.HasPrefix(agentName, "child:") {
		return true
	}

	evidence := newToolResultEvidenceProjection(results)
	return evidence.postChildPacketClosureReady(brief) || evidence.sourceBodyPacketAvailable()
}

func runnerBriefAllowsExternalToolAppend(brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer" {

		return false
	}

	switch strings.TrimSpace(brief.PermissionProfile) {
	case string(workerruntime.PermissionPlanSubmissionReview), string(workerruntime.PermissionReadOnlyVerificationShell):
		return false
	}

	switch strings.TrimSpace(brief.TaskFamily) {
	case PlanThenExecuteReviewRepairTaskFamily, "debug_logs":
		return false
	default:
		return true
	}
}

func (r *runExecution) advertisedAllowedTools(base []string) []string {
	var surface workspace.ToolSurfaceSnapshot
	if r.runner.Workspace != nil {
		surface = r.workspaceToolSurfaceSnapshot()
	}

	return surface.AdvertisedAllowedTools(workspace.AdvertisedToolSurfaceInput{
		BaseTools:        base,
		RuntimeTools:     r.state.runtimeBrief.AllowedTools,
		ConversationOnly: workerruntime.PermissionProfileIsConversationOnly(r.state.runtimeBrief.PermissionProfile),
		SuppressAll: strings.TrimSpace(r.state.runtimeBrief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
			strings.TrimSpace(r.state.runtimeBrief.Contract) == "artifact_final_answer",
		DisableTaskTool: r.runner.DisableTaskTool,
	})
}

func textOnlyChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	limit := textOnlyMaxTokensForBrief(brief)

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func readOnlyEvidenceAvailableTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if !readOnlyEvidenceAvailableTerminalVisibleOutput(brief, results) {
		return opts
	}

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func readOnlyEvidenceAvailableTerminalVisibleOutput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || artifactFinalAnswerBrief(brief) {
		return false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return genericReadOnlyEvidenceReady(brief, results)
}

func textOnlyMaxTokensForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case string(workerruntime.ContractArtifactFinalAnswer):
		return 8192
	case string(workerruntime.ContractDirectAnswer):
		return 1024
	case string(workerruntime.ContractGroundedExplanation), string(workerruntime.ContractGroundedEvidence), string(workerruntime.ContractLocateTargets):
		return 1536
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return 2048
	default:
		return 768
	}
}

// IsTextualToolCall returns true when text looks like tool-call markup
// that should never reach the user-facing presentation layer.
func IsTextualToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	if workspace.LooksLikeXMLToolCallMarkup(lower) ||
		strings.Contains(lower, "[calling tool:") {

		return true
	}

	return looksLikeLineOrientedToolRequest(lower)
}

func looksLikeLineOrientedToolRequest(lower string) bool {
	lines := strings.Split(lower, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "tool:") {
		return false
	}

	toolFields := 0

	for _, line := range lines[1:] {
		field := strings.TrimSpace(line)
		if strings.Contains(field, `"tool"`) {
			return true
		}

		if strings.HasPrefix(field, "{") && strings.Contains(field, `"tool"`) {
			return true
		}

		switch {
		case strings.HasPrefix(field, "capability:"),
			strings.HasPrefix(field, "path:"),
			strings.HasPrefix(field, "pattern:"),
			strings.HasPrefix(field, "query:"),
			strings.HasPrefix(field, "task:"),
			strings.HasPrefix(field, "mode:"),
			strings.HasPrefix(field, "limit:"),
			strings.HasPrefix(field, "command:"),
			strings.HasPrefix(field, "args:"):
			toolFields++
		}
	}

	return toolFields > 0
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if workspaceWriteResultCommitted(req, result) {
		return true
	}
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == workspace.ToolNameTaskLedger {
		return strings.TrimSpace(result.Output) != ""
	}
	if workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
}

func briefIsDocDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		if !writePathIsNonSource(p) {
			return false
		}
	}

	return true
}

func briefIsScratchpadDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		normalized := NormalizeRunnerPath(p)
		if !strings.HasPrefix(normalized, ".idiom/refine/") {
			return false
		}
	}

	return true
}

func scopedImplementationUsesBoundedSourceEdits(brief workerruntime.WorkerRuntimeBrief, paths []string) bool {
	if !runnerBriefRequiresMutation(brief) || len(paths) == 0 || briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || writePathIsNonSource(normalized) {
			continue
		}

		if recoverySearchPathLooksEditableSource(normalized) {
			return true
		}
	}

	return false
}

// writePathIsNonSource returns true for paths whose verification has
// no source-aware analog. Conservative list — adding extensions risks
// silently bypassing real verification gates.
func writePathIsNonSource(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}

	return strings.HasSuffix(p, ".md") ||
		strings.HasSuffix(p, ".markdown") ||
		strings.HasSuffix(p, ".txt") ||
		strings.HasSuffix(p, ".rst")
}

func (r *runExecution) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {

	trimmed := strings.TrimSpace(reply.Content)
	if trimmed != "" {
		if IsTextualToolCall(trimmed) {
			return r.terminalStatusCloseoutAnswer(r.agentName()+" agent emitted textual tool-call markup instead of a final answer.", collectedResults), nil
		}

		r.state.lastAssistantMessage = reply

		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, r.assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: emptyAnswerRetryPrompt(r.state.runtimeBrief),
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.state.runtimeBrief)
	requestMessages := r.chatMessagesForOptions(retryMessages, textOnlyOpts)

	retryMessage, err := r.runner.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "empty_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, textOnlyOpts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return r.terminalStatusCloseoutAnswer(r.agentName()+" agent emitted a tool request after the empty-answer retry.", collectedResults), nil
	}

	if IsTextualToolCall(retryReply) {
		return r.terminalStatusCloseoutAnswer(r.agentName()+" agent emitted textual tool-call markup after the empty-answer retry.", collectedResults), nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		return r.terminalStatusCloseoutAnswer(r.agentName()+" agent emitted a tool request after the empty-answer retry.", collectedResults), nil
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		return r.terminalStatusCloseoutAnswer(r.agentName()+" agent returned an empty response after the final retry.", collectedResults), nil
	}

	r.state.lastAssistantMessage = retryMessage

	return retryTrimmed, nil
}

func extractRunnerFilePath(summary string) string {
	path := extractReadFilePath(summary)
	if path == "" {
		return ""
	}

	fields := strings.Fields(path)
	if len(fields) >= 2 && looksLikeRunnerFileHandle(fields[0]) {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	return path
}

func extractSemanticInspectionPath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}

	for _, marker := range []string{" lines ", ". Use "} {
		idx := strings.Index(trimmed, marker)
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			break
		}
	}

	for _, prefix := range []string{"Read ", "Extracted ", "Outlined "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}

		for _, separator := range []string{" in ", " from "} {
			idx := strings.LastIndex(trimmed, separator)
			if idx <= len(prefix) {
				continue
			}

			return strings.TrimSpace(trimmed[idx+len(separator):])
		}
	}

	return ""
}

func looksLikeRunnerFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}

	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// rewriteVisibilityNote returns a short system-message body explaining that
// the requested tool was redirected to a different tool by harness routing
// rules. Generic across rewrite kinds (delegated bootstrap, verification
// bootstrap, debug-log preflight, etc.) — it states the fact without
// hard-picking the next tool, leaving the model free to act on the result.
func rewriteVisibilityNote(originalTool, rewrittenTool string) string {
	original := strings.TrimSpace(originalTool)
	rewritten := strings.TrimSpace(rewrittenTool)

	switch {
	case original != "" && rewritten != "" && original != rewritten:
		return fmt.Sprintf("Note: your `%s` call was redirected to `%s` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `%s`.", original, rewritten, original)
	case rewritten != "":
		return fmt.Sprintf("Note: your tool call was redirected to `%s` by lane routing. The rewritten call will execute and return its result.", rewritten)
	default:
		return "Note: your tool call was rewritten by lane routing. The rewritten call will execute and return its result."
	}
}

// taskLedgerClosureHint projects unresolved in-progress work from the
// authoritative run-owned ledger. It never parses the rendered ToolResult.
func taskLedgerClosureHint(ledger *taskLedgerState) string {
	if ledger == nil {
		return ""
	}

	var openItems []workspace.TaskLedgerItem
	for _, item := range ledger.items {
		if item.Status == taskledger.StatusInProgress {
			openItems = append(openItems, item)
		}
	}

	if len(openItems) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Task ledger still has typed in_progress obligations while tools are disabled. Do not claim they completed; report them as unresolved in the final status:\n")

	for _, item := range openItems {
		fmt.Fprintf(&b, "- id=%q content=%q status=%q\n", strings.TrimSpace(item.ID), strings.TrimSpace(item.Content), string(item.Status))
	}

	return strings.TrimRight(b.String(), "\n")
}

// toolFailurePathStem returns the first two path segments of the request's
// target path (e.g. "internal/llm" for "internal/llm/llm.go"). Used by the
// same-stem failure counter so that five different hallucinated filenames
// inside the same made-up directory count as repeated failures, not fresh
// attempts.
func toolFailurePathStem(req workspace.ToolRequest) string {
	raw := strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/")

	raw = strings.Trim(raw, "/")

	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}

	return parts[0]
}

// recoveryNudgeInputs bundles the rejection-recovery-nudge state so
// each rule can be unit-tested in isolation without a 7-arg signature.

const (
	reasoningAnswerSalvageMinChars = 40
	reasoningAnswerSalvageMaxChars = 4000
)

// salvageAnswerFromReasoning extracts answer prose from the thinking block
// when the model produced empty visible content but substantial reasoning.
// This happens with Qwen in thinking-enabled mode when the model puts its
// entire response into the reasoning block and leaves Content empty.
func (r *runExecution) salvageAnswerFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return "", false
	}

	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return "", false
	}

	// Only apply to lanes that can produce a direct answer (conversation,
	// read-only). Mutation lanes need tool-call-shaped recovery, not prose.
	if runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return "", false
	}

	// Reasoning must be substantial enough to plausibly contain an answer.
	if len(reasoning) < 200 {
		return "", false
	}

	answer := extractAnswerProseFromReasoning(reasoning)
	if len(strings.TrimSpace(answer)) < reasoningAnswerSalvageMinChars {
		return "", false
	}

	if !reasoningAnswerCandidateIsClean(answer) {
		return "", false
	}

	if blockerKind, _ := r.finalAnswerBlockerKind(answer, results); blockerKind != "" {
		return "", false
	}

	return answer, true
}

// extractAnswerProseFromReasoning extracts the most likely answer prose
// from a model thinking block. Qwen thinking output typically contains
// internal monologue, code inspection, tool-call syntax, and a synthesis
// paragraph where the model works out what to tell the user. This function
// strips the scaffolding and returns the synthesis.
func extractAnswerProseFromReasoning(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}

	if sections, hasMarkers := markedReasoningAnswerSections(reasoning); hasMarkers {
		return selectMarkedReasoningAnswerSection(sections)
	}

	// Phase 1: strip fenced code blocks, Qwen XML tool calls,
	// tool-call wrappers, and internal-monologue lines.
	var cleaned strings.Builder

	inFence := false

	for _, line := range strings.Split(reasoning, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}

		if inFence {
			continue
		}

		if looksLikeQwenThinkingToolCall(trimmed) {
			continue
		}

		if trimmed == "" {
			cleaned.WriteString("\n")

			continue
		}

		if looksLikeToolCallWrapper(trimmed) {
			continue
		}

		if looksLikeInternalMonologue(trimmed) {
			continue
		}

		cleaned.WriteString(line)
		cleaned.WriteString("\n")
	}

	text := cleaned.String()

	// Phase 2: score each paragraph by how answer-like it is and
	// select the best candidate.
	paragraphs := splitReasoningParagraphs(text)
	if len(paragraphs) == 0 {
		return ""
	}

	best := ""
	bestScore := 0

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) < 40 {
			continue
		}

		if looksLikeMetadataBlock(p) {
			continue
		}

		if looksLikeUnfinishedReasoningPlan(p) {
			continue
		}

		score := answerParagraphScore(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}

	if !reasoningAnswerCandidateIsClean(best) {
		return ""
	}

	return best
}

func markedReasoningAnswerSections(reasoning string) ([]string, bool) {
	var (
		sections []string
		current  strings.Builder
	)

	active := false
	seenMarker := false

	for _, line := range strings.Split(reasoning, "\n") {
		if rest, ok := finalAnswerSectionMarkerRest(line); ok {
			seenMarker = true

			if active {
				sections = append(sections, current.String())
				current.Reset()
			}

			active = true

			if rest != "" {
				current.WriteString(rest)
				current.WriteString("\n")
			}

			continue
		}

		if active {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}

	if active {
		sections = append(sections, current.String())
	}

	return sections, seenMarker
}

func finalAnswerSectionMarkerRest(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)

	for _, marker := range []string{"[final answer]", "[answer]"} {
		if strings.HasPrefix(lower, marker) {
			rest := strings.TrimSpace(trimmed[len(marker):])

			rest = strings.TrimLeft(rest, ":.- ")

			return strings.TrimSpace(rest), true
		}
	}

	for _, marker := range []string{"final answer:", "answer:"} {
		if strings.HasPrefix(lower, marker) {
			return strings.TrimSpace(trimmed[len(marker):]), true
		}
	}

	return "", false
}

func selectMarkedReasoningAnswerSection(sections []string) string {
	var (
		selected    string
		selectedKey string
	)

	for _, section := range sections {
		candidate := strings.TrimSpace(section)
		if !reasoningAnswerCandidateIsClean(candidate) {
			return ""
		}

		key := normalizeReasoningAnswerForDedupe(candidate)
		if key == "" {
			return ""
		}

		if selected == "" {
			selected = candidate
			selectedKey = key

			continue
		}

		if key != selectedKey {
			return ""
		}
	}

	return selected
}

func normalizeReasoningAnswerForDedupe(answer string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
}

func reasoningAnswerCandidateIsClean(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return false
	}

	if len(answer) > reasoningAnswerSalvageMaxChars {
		return false
	}

	if finalAnswerSectionMarkerCount(answer) > 0 {
		return false
	}

	if reasoningAnswerLooksTruncated(answer) {
		return false
	}

	if IsTextualToolCall(answer) ||
		looksLikeRawToolResultTranscript(answer) ||
		looksLikeUnexecutedToolRequestAnswer(answer) ||
		looksLikeNonAnswerProse(answer) {

		return false
	}

	return true
}

func reasoningAnswerLooksTruncated(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return true
	}

	last := answer[len(answer)-1]
	switch last {
	case '.', '!', '?', ')', ']', '`', '"', '\'':
		return false
	default:
		return true
	}
}

func splitReasoningParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")

	var out []string

	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		lines := strings.Split(block, "\n")

		var joined []string

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			joined = append(joined, line)
		}

		if len(joined) > 0 {
			out = append(out, strings.Join(joined, " "))
		}
	}

	return out
}

func answerParagraphScore(p string) int {
	score := 0
	lower := strings.ToLower(p)

	for _, word := range []string{
		"because", "therefore", "should", "would", "could",
		"recommend", "suggest", "however", "although",
		"benefit", "tradeoff", "alternative", "approach",
		"important", "useful", "valuable", "better",
		"evidence", "system", "struct", "field",
	} {
		if strings.Contains(lower, word) {
			score++
		}
	}

	if strings.Contains(lower, "you") || strings.Contains(lower, "your") {
		score += 3
	}

	if strings.HasSuffix(strings.TrimSpace(p), ".") ||
		strings.HasSuffix(strings.TrimSpace(p), "?") ||
		strings.HasSuffix(strings.TrimSpace(p), "!") {

		score += 2
	}

	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "looking at", "checking",
		"first,", "next,", "then,", "finally,",
	} {
		if strings.HasPrefix(lower, prefix) {
			score -= 5
		}
	}

	if len(p) > 100 {
		score += 2
	}

	if len(p) > 200 {
		score += 1
	}

	return score
}

func looksLikeQwenThinkingToolCall(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "<") {
		return false
	}

	rest := trimmed[1:]
	if idx := strings.IndexAny(rest, " />"); idx > 0 {
		name := strings.TrimSpace(rest[:idx])
		if knownToolName(name) {
			return true
		}
	}

	if strings.HasPrefix(trimmed, "</") {
		return true
	}

	return false
}

func knownToolName(name string) bool {
	name = strings.TrimSpace(name)
	return workspace.IsBuiltInToolName(name) || name == noteToolName
}

func looksLikeToolCallWrapper(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	if strings.Contains(trimmed, "<tool_call>") || strings.Contains(trimmed, "</tool_call>") {
		return true
	}

	if strings.Contains(trimmed, "<function=") || strings.Contains(trimmed, "</function>") {
		return true
	}

	if strings.HasPrefix(trimmed, "tool:") || strings.HasPrefix(trimmed, "TOOL:") {
		return true
	}

	if strings.HasPrefix(trimmed, `{"tool":`) {
		return true
	}

	return false
}

func looksLikeInternalMonologue(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "i'm going to", "i would",
		"i could", "i might", "i have", "i've", "i had", "i'd",
		"looking at", "checking", "i notice", "i see", "i found",
		"first,", "next,", "then,", "finally,", "also,",
		"this means", "so the", "so i", "so we",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func looksLikeMetadataBlock(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return true
	}

	if looksLikeStructuredInternalState(p) {
		return true
	}

	if !strings.Contains(p, " ") && len(p) < 20 {
		return true
	}

	if len(p) > 10 && strings.ToUpper(p) == p {
		return true
	}

	alpha := 0

	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			alpha++
		}
	}

	if alpha < 3 && len(p) > 2 {
		return true
	}

	return false
}

func looksLikeUnfinishedReasoningPlan(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return true
	}

	taskFramingPrefixes := []string{
		"the user wants me",
		"the user asked me",
		"user wants me",
		"user asked me",
	}
	for _, prefix := range taskFramingPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ContainsAny(lower,
				" i need to ",
				" i should ",
				" i have to ",
				" need to ",
				" should ",
				" find ",
				" inspect ",
				" check ",
				" debug ",
				" understand ",
				" gather ",
			)
		}
	}

	if strings.Contains(lower, " i need to ") || strings.Contains(lower, " i should ") {
		return !looksLikeGroundedAnswer(lower)
	}

	return false
}

func looksLikeGroundedAnswer(lower string) bool {
	return strings.Contains(lower, "root cause") ||
		strings.Contains(lower, "because") ||
		strings.Contains(lower, "evidence") ||
		strings.Contains(lower, ".go:") ||
		strings.Contains(lower, ".py:") ||
		strings.Contains(lower, ".ts:") ||
		strings.Contains(lower, ".rs:") ||
		strings.Contains(lower, ".java:") ||
		strings.Contains(lower, ".cpp:") ||
		strings.Contains(lower, ".h:")
}

func looksLikeStructuredInternalState(p string) bool {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"current_state:", "tool_manifest:", "allowed_tools:", "forbidden_tools:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	first := trimmed[0]
	if first != '{' && first != '[' {
		return false
	}

	hasInternalStateMarker := strings.Contains(lower, `"current_state"`) ||
		strings.Contains(lower, `"tool_manifest"`) ||
		strings.Contains(lower, `"allowed_tools"`) ||
		strings.Contains(lower, `"forbidden_tools"`)

	return hasInternalStateMarker
}

func (r *runExecution) visibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	completion := implementationCompletionState{}
	if r != nil {
		completion = r.implementationCompletionState(results)
	}
	mutationCommitted := completion.HasChanges()
	visible := runnerVisibleBriefForToolSurface(brief, results, completion)
	if r == nil || r.runner.Workspace == nil ||
		(!exactAnchorPreWriteSurfaceActive(brief, results, mutationCommitted) &&
			!implementationTargetSourcePending(brief, results, mutationCommitted) &&
			!implementationTargetWriteReadySurfaceActive(brief, results, mutationCommitted) &&
			!implementationScopedEditSurfaceActive(brief, results, completion.MissingRequiredPaths, mutationCommitted)) {

		return visible
	}

	baseAllowed := r.workspaceAdvertisedAllowedTools()
	allowed := workspace.ToolSurfacePhaseAllowedTools(baseAllowed, workspace.ToolSurfacePhaseImplementationPreWrite)
	if implementationTargetSourcePending(brief, results, mutationCommitted) {
		allowed = workspace.ToolSurfacePhaseAllowedTools(baseAllowed, workspace.ToolSurfacePhaseImplementationTargetSource)
	} else if implementationTargetWriteReadySurfaceActive(brief, results, mutationCommitted) {
		allowed = implementationTargetWriteReadyAllowedToolsForBrief(baseAllowed, brief, results, mutationCommitted)
	} else if implementationScopedEditSurfaceActive(brief, results, completion.MissingRequiredPaths, mutationCommitted) {
		allowed = workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(baseAllowed, r.externalReadOnlyDiscoveryTool)
	}
	allowed = textutil.Merge(
		allowed,
		r.permissionAuthorizedInspection(baseAllowed).ToolNames(),
	)

	visible.AllowedTools = allowed
	visible.ToolManifest = nil

	if len(allowed) > 0 {
		visible.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	return workerruntime.NormalizeRuntimeBrief(visible)
}

func (r *runExecution) textualToolProtocolPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	if r == nil || r.runner.Client == nil || r.runner.Client.SupportsNativeTools() {
		return ""
	}

	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return ""
	}

	entries := textualToolProtocolEntries(brief)

	var b strings.Builder

	b.WriteString("# Textual Tool Call Protocol\n\n")
	b.WriteString("Native tool calls are unavailable for this backend. Idiom will execute tools only when your entire reply is one textual JSON tool request.\n\n")
	b.WriteString("When taking a tool action, reply with exactly one JSON object for a real allowed tool call and no prose, markdown commentary, XML tags, or second JSON object. Do not copy generic field names or schema examples as values.\n\n")
	b.WriteString("Keep structured fields structured: arrays and objects must be JSON arrays/objects, not quoted JSON strings. For `submit_plan`, `edits` must be an array and every edit object must include `path`.\n")
	b.WriteString("Use the runtime brief's `tool_manifest` for allowed tools and fields. If the runtime brief requires a tool, do not answer in prose while that tool contract is still open.\n")

	if planThenExecutePlannerBrief(brief) && toolManifestHasTool(brief.ToolManifest, workspace.ToolNameSubmitPlan) {
		b.WriteString("\nFor plan-execute `submit_plan`, emit one JSON object with top-level `tool`, `rationale`, and `edits`. Use only real paths and complete current/final source text from gathered evidence; never copy placeholders, ellipses, `TODO`, schema examples, or `existing code` comments.\n")
	}

	if len(entries) > 0 {
		b.WriteString("\nCurrent required/allowed examples:\n")

		for _, entry := range entries {
			b.WriteString("- ")

			tool := strings.TrimSpace(entry.Tool)
			b.WriteString(tool)

			if req := strings.TrimSpace(entry.Request); req != "" {
				b.WriteByte(' ')
				b.WriteString(req)
			}

			if purpose := strings.TrimSpace(entry.Purpose); purpose != "" {
				b.WriteString(" (")
				b.WriteString(purpose)
				b.WriteString(")")
			}

			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

func toolManifestHasTool(manifest []workspace.ToolManifestEntry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	for _, entry := range manifest {
		if strings.TrimSpace(entry.Tool) == name {
			return true
		}
	}

	return false
}

func textualToolProtocolEntries(brief workerruntime.WorkerRuntimeBrief) []workspace.ToolManifestEntry {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return nil
	}

	suppressWholeFileWrites := scopedImplementationUsesBoundedSourceEdits(brief, brief.RequiredEditPaths)
	required := make(map[string]struct{}, len(brief.RequiredTools))

	for _, tool := range brief.RequiredTools {
		for _, concrete := range workspace.ConcreteRequiredTools(tool) {
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(concrete) {
				continue
			}

			if concrete != "" {
				required[concrete] = struct{}{}
			}
		}
	}

	selected := make([]workspace.ToolManifestEntry, 0, len(brief.ToolManifest))
	seenEntries := make(map[string]struct{}, len(brief.ToolManifest))
	seenTools := make(map[string]struct{}, len(brief.ToolManifest))

	if len(required) > 0 {
		for _, entry := range brief.ToolManifest {
			tool := strings.TrimSpace(entry.Tool)
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
				continue
			}

			if _, ok := required[tool]; ok {
				key := textualToolProtocolEntryKey(entry)
				if _, seen := seenEntries[key]; seen {
					continue
				}

				selected = append(selected, entry)
				seenEntries[key] = struct{}{}
				seenTools[tool] = struct{}{}
			}
		}
	}

	const maxExamples = 8

	for _, entry := range brief.ToolManifest {
		if len(selected) >= maxExamples {
			break
		}

		tool := strings.TrimSpace(entry.Tool)
		if tool == "" {
			continue
		}

		if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
			continue
		}

		if _, ok := seenTools[tool]; ok {
			continue
		}

		key := textualToolProtocolEntryKey(entry)
		if _, seen := seenEntries[key]; seen {
			continue
		}

		selected = append(selected, entry)
		seenEntries[key] = struct{}{}
		seenTools[tool] = struct{}{}
	}

	return selected
}

func textualToolProtocolWholeFileWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return true
	default:
		return false
	}
}

func textualToolProtocolEntryKey(entry workspace.ToolManifestEntry) string {
	return strings.TrimSpace(entry.Tool) + "\x00" + strings.TrimSpace(entry.Request)
}

func workspaceWriteResultCommitted(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	receipt := result.CommitReceipt()
	return receipt != nil && len(receipt.DurableProjection().Effects) > 0
}

func workspaceWriteResultFullyCommitted(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return workspaceWriteResultCommitted(req, result) && strings.TrimSpace(result.Error) == ""
}

func exactAnchorImplementationActive(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	return slices.Contains(brief.Constraints, "exact_anchor_implementation")
}

func normalizedRequiredEditPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}

	return out
}

func readRequestTargetsRequiredEditPath(req workspace.ToolRequest, requiredPaths []string) bool {
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if _, ok := required[path]; ok {
		return true
	}

	return false
}

func scopedReadRecoveryRejectionKey(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	target := NormalizeRunnerPath(textutil.FirstNonEmpty(req.Path, req.ID, req.Handle, req.Query, req.Pattern, req.Task, req.Command))

	if target == "" {
		target = "<empty-target>"
	}

	return tool + ":" + normalizeActionToken(target)
}

func requiredEditPathAllowsMkdir(dir string, required map[string]struct{}) bool {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return false
	}

	for path := range required {
		parent := strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Dir(path))), "/")
		if parent == dir || strings.HasPrefix(parent, dir+"/") {
			return true
		}
	}

	return false
}

func nonEmptyRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) == 0 {
		return nil
	}

	out := make([]string, 0, max)

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		out = append(out, trimmed)
		if len(out) >= max {
			return out
		}
	}

	return out
}

func hasSuccessfulVerificationResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.AnalyzeVerificationResult(result).Successful {
			return true
		}
	}

	return false
}

func hasSuccessfulTaskResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func postChildReadBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return 3
	}

	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return 0
	}

	if runnerBriefThoroughness(brief) == "standard" {
		return 1
	}

	return 2
}

func runnerBriefThoroughness(brief workerruntime.WorkerRuntimeBrief) string {
	for _, constraint := range brief.Constraints {
		key, value, ok := strings.Cut(strings.TrimSpace(constraint), "=")
		if !ok || strings.TrimSpace(key) != "thoroughness" {
			continue
		}

		return strings.ToLower(strings.TrimSpace(value))
	}

	return ""
}

func successfulReadClassResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		req := workspace.ToolRequest{Tool: strings.TrimSpace(result.Tool)}
		if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			count++
		}
	}

	return count
}

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Args is `json:"-"` on ToolRequest (it intentionally stays out of
	// log/event serialization), so it would NOT contribute to this key
	// otherwise. For MCP tools and external tools the typed fields are
	// empty and Args carries the entire request shape — without this
	// suffix two materially-different calls (e.g. `lookup` with
	// {"enum":"JobType"} vs {"enum":"ColonistState"}) would collapse to
	// the same key and trip the "replayed prior evidence" guard.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func normalizedToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Mirror the toolRequestKey suffix: Args is `json:"-"` on
	// ToolRequest so the marshal above misses it. The doom_loop_guard
	// and other fingerprint-based heuristics need to distinguish
	// external/MCP calls that carry their entire request shape in
	// Args; without this, three different lookup({enum:X}),
	// lookup({enum:Y}), lookup({enum:Z}) calls all collapse to one
	// fingerprint and trip the doom_loop_guard at threshold 3.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func doomLoopForcedCloseoutPrompt(req workspace.ToolRequest, identicalCallRun int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the same tool"
	}

	return fmt.Sprintf("The exact same %s request was stopped after %d identical attempts. Tools are now disabled. Complete the active contract from the evidence already gathered. If the evidence is sufficient, answer with the required final decision or summary. If one concrete claim remains unsupported, answer with <<QUESTION>> and the exact unresolved blocker; do not request more tools.", tool, identicalCallRun)
}

func shouldRejectExactReplayBeforeLoopGate(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return true
	default:
		return false
	}
}

func normalizeRunnerToolRequest(req workspace.ToolRequest) workspace.ToolRequest {
	req.Path = canonicalFingerprintPath(req.Path)
	req.Destination = canonicalFingerprintPath(req.Destination)
	req.Workdir = canonicalFingerprintPath(req.Workdir)
	req.NamedGap = ""
	req.WhyNotAnswering = ""

	req.Moves = append([]workspace.GoDeclarationMove(nil), req.Moves...)
	for i := range req.Moves {
		req.Moves[i].Destination = canonicalFingerprintPath(req.Moves[i].Destination)
	}
	req.Edits = append([]planartifact.PlanEdit(nil), req.Edits...)
	for i := range req.Edits {
		req.Edits[i].Path = canonicalFingerprintPath(req.Edits[i].Path)
	}
	req.SourceSatisfiedPaths = append([]string(nil), req.SourceSatisfiedPaths...)
	for i := range req.SourceSatisfiedPaths {
		req.SourceSatisfiedPaths[i] = canonicalFingerprintPath(req.SourceSatisfiedPaths[i])
	}
	if strings.TrimSpace(req.Tool) == workspace.ToolNameApplyPatch {
		req.Content = canonicalFingerprintPatch(req.Content)
	}

	return req
}

func canonicalFingerprintPath(path string) string {
	if canonical := cleanRelativeRunnerPath(path); canonical != "" {
		return canonical
	}

	return NormalizeRunnerPath(path)
}

func canonicalFingerprintPatch(content string) string {
	ops, err := patching.Parse(content)
	if err != nil {
		return content
	}

	for i := range ops {
		path := cleanRelativeRunnerPath(ops[i].Path)
		if path == "" {
			return content
		}
		ops[i].Path = path
		if strings.TrimSpace(ops[i].MoveTo) == "" {
			continue
		}
		moveTo := cleanRelativeRunnerPath(ops[i].MoveTo)
		if moveTo == "" {
			return content
		}
		ops[i].MoveTo = moveTo
	}

	return patching.Serialize(ops)
}

// truncForRetryReason trims a summary so the stored "why this was
// blocked" string stays small — model-facing text, not log-sized.
func truncForRetryReason(s string) string {
	s = strings.TrimSpace(s)

	const max = 160

	if len(s) <= max {
		return s
	}

	return s[:max-3] + "..."
}

const narrowToolFamilyNudgeAt = 5

func toolActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	if languagetools.IsToolName(tool) {
		target := textutil.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	}

	switch tool {
	case workspace.ToolNameListFiles:
		if workspace.IsScopedInspectionRequest(req) {
			return "list_files:" + normalizeActionToken(req.Path)
		}

		return "broad_inspection"
	case workspace.ToolNameShell:
		return shellActionClass(req.Command)
	case workspace.ToolNameReadFile:
		if target := textutil.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path)); target != "" {
			action := "read_file:" + normalizeActionToken(target)
			if strings.TrimSpace(req.ID) == "" && (req.StartLine > 0 || req.EndLine > 0) {
				action += fmt.Sprintf(":%d-%d", req.StartLine, req.EndLine)
			}

			return action
		}

		return workspace.ToolNameReadFile
	case workspace.ToolNameSourceSanity:
		return tool + ":" + normalizeActionToken(req.Path)
	case workspace.ToolNameCodeNavigate:
		target := textutil.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameReadOnlyCapability:
		target := textutil.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameEditFile:
		shape := strings.TrimSpace(req.OldText) + "->" + strings.TrimSpace(req.NewText)
		if req.StartLine > 0 || req.EndLine > 0 || strings.TrimSpace(req.Content) != "" {
			shape = fmt.Sprintf("%d-%d:%s", req.StartLine, req.EndLine, strings.TrimSpace(req.Content))
		}

		return "edit_file:" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(shape)
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
	case workspace.ToolNameDiscoveryQuery:
		purpose := textutil.FirstNonEmpty(strings.TrimSpace(req.Purpose), strings.TrimSpace(req.Query))
		return tool + ":" + normalizeActionToken(purpose) + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameStructuralSearch:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameDependencyGraph:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameHistoryRange, workspace.ToolNameBlameRange:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(fmt.Sprintf("%d-%d", req.StartLine, req.EndLine))
	case workspace.ToolNameHistorySymbol, workspace.ToolNameCtagsSymbols, workspace.ToolNameSemgrepScan:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern)
	case workspace.ToolNameJoernScan:
		return tool + ":" + normalizeActionToken(req.Path)
	case workspace.ToolNameGoMoveDeclarations:
		target := textutil.FirstNonEmpty(strings.TrimSpace(req.Path)+"->"+strings.TrimSpace(req.Destination), strings.TrimSpace(req.Pattern))
		if len(req.Moves) > 0 {
			target = strings.TrimSpace(req.Path) + "->batch"
		}

		return tool + ":" + normalizeActionToken(target)
	case workspace.ToolNameTaskLedger:
		return workspace.ToolNameTaskLedger
	default:
		return tool
	}
}

func isBootstrappedTargetReadReminder(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
		strings.TrimSpace(result.Summary) == "Implementation bootstrap already inspected this target" &&
		strings.TrimSpace(result.Error) != ""
}

func declarationMovesForbiddenByMessages(messages []llama.ChatMessage) bool {
	if len(messages) == 0 {
		return false
	}

	lastUser := -1

	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == "user" {
			lastUser = i
			break
		}
	}

	if lastUser < 0 {
		return false
	}

	for _, message := range messages[lastUser:] {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "system" {
			continue
		}

		if textForbidsDeclarationMoves(message.Content) {
			return true
		}
	}

	return false
}

func textForbidsDeclarationMoves(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))

	return ContainsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func declarationMoveConstraintRejection(req workspace.ToolRequest, declarationMovesForbidden bool) (string, string, bool) {
	if !declarationMovesForbidden {
		return "", "", false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameGoMoveDeclarations:
		return "Declaration move rejected by task constraint",
			"the current task explicitly forbids moving declarations. Do not call go_move_declarations; allowed contract outcomes are narrow same-file read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	case "go_file_outline":
		return "Go outline rejected by task constraint",
			"the current task explicitly forbids declaration moves, and go_file_outline primarily suggests declaration-move batches. Do not call go_file_outline again; allowed contract outcomes are exact-line read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	default:
		return "", "", false
	}
}

func marshalToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}

	return string(data)
}

func shellActionClass(command string) string {
	analysis := workspace.AnalyzeShellCommand(command)
	if analysis.Verification != workspace.ShellVerificationNone {
		return "shell_verification:" + normalizeActionToken(command)
	}

	if len(analysis.Fields) == 0 {
		return workspace.ToolNameShell
	}

	switch {
	case analysis.BroadInspection:
		return "broad_inspection"
	case analysis.Narrowing:
		return "shell_narrow:" + normalizeActionToken(command)
	default:
		return "shell:" + normalizeActionToken(analysis.Fields[0])
	}
}

func normalizeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "_"
	}

	if len(value) > 96 {
		return value[:96]
	}

	return value
}

func runnerPlanExecuteTextualAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, inspection workspace.PermissionAuthorizedInspectionProjection, authority planThenExecutePathAuthority) []string {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	input := workspace.PlanExecuteTextualAllowedToolsInput{
		TaskFamily:          taskFamily,
		CurrentAllowedTools: currentAllowedTools,
		BriefAllowedTools:   brief.AllowedTools,
	}
	if taskFamily == PlanThenExecuteReviewRepairTaskFamily {
		if !completionGateResultSeen(results) && len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) == 0 {
			input.ReviewRepairInitialProbe = true
			return textutil.Merge(workspace.PlanExecuteTextualAllowedTools(input), inspection.ToolNames())
		}

		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			PhaseHint:           contractcompile.TurnPhaseReviewRepairTerminal,
			Brief:               brief,
			Results:             results,
			InspectionAuthority: inspection,
			BaseAllowedTools:    brief.AllowedTools,
		})
		input.ReviewRepairTerminalAllowedTools = contract.AllowedTools
		return textutil.Merge(workspace.PlanExecuteTextualAllowedTools(input), inspection.ToolNames())
	}

	if taskFamily != PlanThenExecutePlannerTaskFamily {
		return textutil.Merge(workspace.PlanExecuteTextualAllowedTools(input), inspection.ToolNames())
	}

	state, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results, authority)
	input.BaseTools = baseTools
	input.CallableTools = state.CallableTools
	input.ParserVisibleTools = state.ParserVisibleTools
	input.TerminalOptions = state.TerminalOptions
	input.SourceSatisfiedTerminalComplete = planExecuteSourceSatisfiedTerminalEvidenceComplete(brief, results, state, authority)
	input.TerminalSubmitPlanAllowed = planThenExecuteTerminalSubmitPlanAllowed(brief, results, authority)
	allowed := workspace.PlanExecuteTextualAllowedTools(input)
	if input.TerminalSubmitPlanAllowed {
		allowed = textutil.Merge(allowed, []string{workspace.ToolNameSubmitPlan})
	}
	return textutil.Merge(allowed, inspection.ToolNames())
}

func (r *runExecution) runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest, inspection workspace.PermissionAuthorizedInspectionProjection, authority planThenExecutePathAuthority) []string {
	allowed := runnerPlanExecuteTextualAllowedTools(currentAllowedTools, brief, results, inspection, authority)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return allowed
	}

	if len(textutil.Unique(allowed)) == 1 && slices.Contains(allowed, workspace.ToolNameSubmitPlan) {
		return allowed
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || slices.Contains(allowed, tool) {
		return allowed
	}

	state, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results, authority)
	if planExecuteSourceSatisfiedTerminalEvidenceComplete(brief, results, state, authority) {
		return allowed
	}

	// A submit_plan that is otherwise grounded and in scope must reach the
	// typed history-prerequisite owner. Hiding it here turns the exact model
	// request into a phase rejection, so no DeferredPlanPaths ledger is created
	// and the later history result cannot restore that intent. Permission and
	// configured policy are still evaluated by the normal dispatch pipeline.
	if tool == workspace.ToolNameSubmitPlan && slices.Contains(baseTools, workspace.ToolNameSubmitPlan) && r != nil {
		_, historyPending := r.submitPlanHistoryPendingForRequest(req, results)
		if historyPending {
			return textutil.Merge(allowed, []string{workspace.ToolNameSubmitPlan})
		}
	}

	return workspace.PlanExecuteTextualAllowedToolsForRequest(allowed, state.ParserVisibleTools, baseTools, tool)
}

func runnerPlanExecuteTextualPlanningProjection(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) (PlanExecutePlanningState, []string) {
	baseTools := workspace.PlanExecuteTextualBaseTools(brief.TaskFamily, brief.AllowedTools, currentAllowedTools)
	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		baseTools = textutil.Unique(append(baseTools, workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)...))
	}

	return state, baseTools
}

func (r *runExecution) salvageToolRequestFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return workspace.ToolRequest{}, "", false
	}

	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return workspace.ToolRequest{}, "", false
	}

	for _, candidate := range reasoningToolRequestCandidates(reasoning) {
		req, raw, ok, err := workspace.ParseToolRequest(candidate)
		if !ok || err != nil {
			continue
		}

		if r.safeReasoningToolRequest(req, results) {
			return req, raw, true
		}
	}

	return workspace.ToolRequest{}, "", false
}

func (r *runExecution) salvageVisibleToolRequestFromProse(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if req, raw, ok := parseVisibleLineToolRequest(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	raw := trimmed[start:end]

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req, parsedRaw, ok := parseVisibleToolRequestJSONObject(raw, fields)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan &&
		!looksLikeUnexecutedToolRequestAnswer(trimmed) &&
		!r.explicitVisibleToolJSONRecoveryAllowed(trimmed, fields, results) {

		return workspace.ToolRequest{}, "", false
	}

	if r.safeVisibleToolRequest(req, results) {
		return req, parsedRaw, true
	}

	return workspace.ToolRequest{}, "", false
}

func parseMalformedVisibleReadOnlyToolJSONPrefix(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, _, ok := firstToolRequestShapedJSONObjectSpan(text); ok {
		return workspace.ToolRequest{}, "", false
	}

	start := strings.Index(text, "{")
	if start < 0 || markdownFenceOpenBefore(text, start) {
		return workspace.ToolRequest{}, "", false
	}

	candidate := text[start:]
	if !strings.Contains(candidate, `"tool"`) {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            firstJSONLikeStringField(candidate, "tool"),
		Path:            firstJSONLikeStringField(candidate, "path"),
		Pattern:         firstJSONLikeStringField(candidate, "pattern"),
		Query:           firstJSONLikeStringField(candidate, "query"),
		Glob:            firstJSONLikeStringField(candidate, "glob"),
		Capability:      firstJSONLikeStringField(candidate, "capability"),
		NamedGap:        firstJSONLikeStringField(candidate, "named_gap"),
		WhyNotAnswering: firstJSONLikeStringField(candidate, "why_not_answering"),
	}
	if v, ok := firstJSONLikeIntField(candidate, "start_line"); ok {
		req.StartLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "end_line"); ok {
		req.EndLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "limit"); ok {
		req.Limit = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "budget_tokens"); ok {
		req.BudgetTokens = v
	}

	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !malformedVisibleReadOnlyToolCanSalvage(req.Tool) {
		return workspace.ToolRequest{}, "", false
	}

	raw := marshalToolRequestRaw(req)

	return req, raw, true
}

func malformedVisibleReadOnlyToolCanSalvage(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile:
		return true
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameDiscoveryQuery, workspace.ToolNameStructuralSearch, workspace.ToolNameFileStats, workspace.ToolNameCodeNavigate:
		return true
	default:
		return false
	}
}

func firstJSONLikeStringField(text string, key string) string {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"((?:\\.|[^"\\])*)"`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return ""
	}

	decoded, err := strconv.Unquote(`"` + match[1] + `"`)
	if err != nil {
		return strings.TrimSpace(match[1])
	}

	return strings.TrimSpace(decoded)
}

func firstJSONLikeIntField(text string, key string) (int, bool) {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"?([0-9]+)"?`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return 0, false
	}

	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}

	return value, true
}

func (r *runExecution) explicitVisibleToolJSONRecoveryAllowed(text string, fields map[string]json.RawMessage, results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) {
		return false
	}

	rawTool, ok := fields["tool"]
	if !ok {
		return false
	}

	var tool string

	if err := json.Unmarshal(rawTool, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return false
	}

	return countToolRequestShapedJSONObjects(text) == 1
}

func countToolRequestShapedJSONObjects(text string) int {
	count := 0

	for searchFrom := 0; searchFrom < len(text); {
		start, end, ok := firstToolRequestShapedJSONObjectSpan(text[searchFrom:])
		if !ok {
			break
		}

		count++

		searchFrom += end

		if start == end {
			searchFrom++
		}
	}

	return count
}

func parseVisibleLineToolRequest(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "{") || strings.Contains(text, "```") {
		return workspace.ToolRequest{}, "", false
	}

	// Scan for the first tool: line, skipping leading prose.
	// Models sometimes emit conversational text before a tool call.
	lines := strings.Split(text, "\n")
	toolLineIdx := -1
	proseLines := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if ok && textutil.EqualTrimFold(key, "tool") {
			toolLineIdx = i
			break
		}

		proseLines++
		if proseLines > 10 {
			return workspace.ToolRequest{}, "", false
		}
	}

	if toolLineIdx < 0 {
		return workspace.ToolRequest{}, "", false
	}

	// Parse from the tool: line onward.
	values := map[string]string{}

	for _, line := range lines[toolLineIdx:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return workspace.ToolRequest{}, "", false
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		if key == "" || value == "" {
			return workspace.ToolRequest{}, "", false
		}

		switch key {
		case "tool", "path", "pattern", "query", "capability", "mode", "task", "named_gap", "why_not_answering",
			"start_line", "end_line", "limit", "budget_tokens":
			values[key] = value
		default:
			return workspace.ToolRequest{}, "", false
		}
	}

	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            tool,
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Capability:      values["capability"],
		Mode:            values["mode"],
		Task:            values["task"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
	}
	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			assign(v)
		}
	}

	return req, marshalToolRequestRaw(req), true
}

func parseVisibleToolRequestJSONObject(raw string, fields map[string]json.RawMessage) (workspace.ToolRequest, string, bool) {
	if _, hasExplicitTool := fields["tool"]; hasExplicitTool {
		req, parsedRaw, ok, err := workspace.ParseToolRequest(raw)
		if !ok || err != nil {
			return workspace.ToolRequest{}, "", false
		}

		return req, parsedRaw, true
	}

	if _, hasEdits := fields["edits"]; hasEdits {
		if _, hasRationale := fields["rationale"]; hasRationale {
			req, err := workspace.DecodeToolRequestJSON([]byte(raw))
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			req.Tool = workspace.ToolNameSubmitPlan

			return req, marshalToolRequestRaw(req), true
		}
	}

	if _, hasPlanArtifact := fields["plan_artifact"]; hasPlanArtifact {
		req, err := workspace.DecodeToolRequestJSON([]byte(raw))
		if err != nil {
			return workspace.ToolRequest{}, "", false
		}

		req.Tool = workspace.ToolNameSubmitPlan

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func (r *runExecution) safeVisibleToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {
		return r.safeVisibleSubmitPlanRequest(req, results)
	}

	if r.safeVisiblePlanExecuteMissingTargetReadRequest(req) {
		return true
	}

	if r.safeVisibleWorkspaceWriteRequest(req, results) {
		return true
	}

	return r.safeReasoningToolRequest(req, results)
}

func (r *runExecution) safeVisibleWorkspaceWriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) || !safeWorkspaceWriteRequestShape(req) {
		return false
	}

	assessment, ok := r.mutationAdmissionForRequest(req)
	if !ok || !assessment.Analysis.Mutation || len(assessment.Analysis.Targets) == 0 {
		return false
	}
	analysis := assessment.Analysis

	if len(r.state.runtimeBrief.RequiredEditPaths) > 0 {
		return r.safeReasoningWorkspaceWriteRequest(req)
	}

	if !completionGateResultSeen(results) {
		return false
	}

	paths := mutationRequestCanonicalTargetIDs(analysis)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !newToolResultEvidenceProjection(results).HasSourceWindow(path) {
			return false
		}
	}

	return true
}

func (r *runExecution) safeVisiblePlanExecuteMissingTargetReadRequest(req workspace.ToolRequest) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	return strings.TrimSpace(req.Path) == "" &&
		strings.TrimSpace(req.ID) == "" &&
		strings.TrimSpace(req.Handle) == ""
}

func (r *runExecution) safeVisibleSubmitPlanRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	if !r.planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results) {
		return false
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}

	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}

	required := r.requiredEditCoveragePaths()
	if len(required) == 0 {
		return len(covered) > 0
	}

	return len(missingRequiredEditPaths(required, covered)) == 0
}

func (r *runExecution) planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	return planThenExecuteTerminalSubmitPlanAllowed(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) ||
		planThenExecuteRejectedPlanCorrectionOnly(r.state.runtimeBrief, results)
}

func reasoningToolRequestCandidates(reasoning string) []string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}

	candidates := make([]string, 0, 3)

	for offset := 0; offset < len(reasoning); {
		idx := strings.Index(reasoning[offset:], "```tool")
		if idx < 0 {
			break
		}

		start := offset + idx
		rest := reasoning[start:]
		end := strings.Index(rest, "\n```")

		if end < 0 {
			break
		}

		candidates = append(candidates, rest[:end+4])
		offset = start + end + 4
	}

	if strings.Contains(reasoning, "<tool_call>") || strings.Contains(reasoning, "<function=") {
		candidates = append(candidates, reasoning)
	}

	if strings.Contains(reasoning, `"tool"`) {
		candidates = append(candidates, reasoning)
	}

	return candidates
}

func (r *runExecution) safeReasoningToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	switch tool {
	case workspace.ToolNameReadFile:
		return strings.TrimSpace(req.Path) != ""
	case workspace.ToolNameSearchContent:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameSearchFiles:
		return strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Glob) != ""
	case workspace.ToolNameContextPack:
		return strings.TrimSpace(req.Task) != "" && !contextPackUnavailableForRecovery(results)
	case workspace.ToolNameContextQuery:
		return strings.TrimSpace(req.Query) != ""
	case workspace.ToolNameContextZoom:
		return strings.TrimSpace(req.Handle) != ""
	case workspace.ToolNameDiscoveryQuery:
		return safeDiscoveryQueryRequest(req)
	case workspace.ToolNameStructuralSearch:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameCodeNavigate:
		if _, err := languagetools.CodeNavigationSpecFor(req.Capability); err != nil {
			return false
		}

		return strings.TrimSpace(req.Path) != "" || strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameSubmitPlan:
		return false
	default:
		return r.safeReasoningWorkspaceWriteRequest(req)
	}
}

func safeDiscoveryQueryRequest(req workspace.ToolRequest) bool {
	if strings.TrimSpace(textutil.FirstNonEmpty(req.Purpose, req.Query, req.Pattern, req.Path)) == "" {
		return false
	}

	if purpose := strings.TrimSpace(req.Purpose); purpose != "" {
		return normalizeRunnerDiscoveryQueryPurpose(purpose) != ""
	}

	if query := strings.TrimSpace(req.Query); query != "" {
		return normalizeRunnerDiscoveryQueryPurpose(query) != ""
	}

	return true
}

func normalizeRunnerDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	switch value {
	case "impact", "impact_analysis":
		return "impact_analysis"
	case "trace", "function_trace", "function_tracing":
		return "function_tracing"
	case "architecture", "architecture_boundary", "boundary", "tool_usefulness", "tooling_usefulness", "discovery_tooling", "discovery_tools", "usefulness":
		return "architecture_boundary"
	case "security", "security_review":
		return "security_review"
	case "history", "history_context":
		return "history_context"
	default:
		if strings.Contains(value, "security") || strings.Contains(value, "vulnerability") {
			return "security_review"
		}

		if strings.Contains(value, "history") || strings.Contains(value, "evolution") {
			return "history_context"
		}

		if strings.Contains(value, "impact") || strings.Contains(value, "blast_radius") {
			return "impact_analysis"
		}

		if strings.Contains(value, "trace") || strings.Contains(value, "tracing") || strings.Contains(value, "call_graph") {
			return "function_tracing"
		}

		if strings.Contains(value, "architecture") ||
			strings.Contains(value, "boundary") ||
			(strings.Contains(value, "discovery") && (strings.Contains(value, "implementation") || strings.Contains(value, "definition") || strings.Contains(value, "orchestration") || strings.Contains(value, "pattern"))) {

			return "architecture_boundary"
		}

		return ""
	}
}

func (r *runExecution) safeReasoningWorkspaceWriteRequest(req workspace.ToolRequest) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) || len(r.state.runtimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	assessment, ok := r.mutationAdmissionForRequest(req)
	if !ok || !assessment.Analysis.Mutation || len(assessment.Analysis.Targets) == 0 {
		return false
	}

	return safeWorkspaceWriteRequestShape(req)
}

func safeWorkspaceWriteRequestShape(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile:
		hasOldNew := strings.TrimSpace(req.OldText) != "" && (strings.TrimSpace(req.NewText) != "" || req.NewText == "")
		hasRange := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""

		return hasOldNew || hasRange
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return strings.TrimSpace(req.Content) != ""
	case workspace.ToolNameApplyPatch:
		if strings.TrimSpace(req.Content) == "" {
			return false
		}

		_, err := patching.Parse(req.Content)

		return err == nil
	default:
		return false
	}
}

func reasoningToolRequestHasPlaceholder(req workspace.ToolRequest) bool {
	fields := []string{
		req.Path,
		req.Task,
		req.Pattern,
		req.Query,
		req.Content,
		req.OldText,
		req.NewText,
		req.NamedGap,
		req.WhyNotAnswering,
		req.Rationale,
	}
	for _, edit := range reasoningSubmitPlanEdits(req) {
		fields = append(fields,
			edit.Path,
			edit.OldText,
			edit.NewText,
			edit.NewTextRef,
			edit.Content,
			edit.ContentRef,
			edit.Purpose,
		)
	}

	for _, field := range fields {
		lower := strings.ToLower(strings.TrimSpace(field))
		if lower == "" {
			continue
		}

		if ContainsAny(lower,
			"candidate/path.go",
			"relative/file",
			"exact current text",
			"exact current substring",
			"replacement text",
			"complete replacement text",
			"why these candidate path edits satisfy the objective",
			"one paragraph on the overall plan",
			"why this edit",
			"one sentence",
			"<full file body>",
			"<user objective>",
			"<path>",
			"<pattern>",
		) {

			return true
		}
	}

	return false
}

func reasoningSubmitPlanEdits(req workspace.ToolRequest) []planartifact.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]planartifact.PlanEdit(nil), req.PlanArtifact.Edits...)
	}

	return append([]planartifact.PlanEdit(nil), req.Edits...)
}

const (
	toolRequestNormalizerOutcomeNormalized          = "normalized"
	toolRequestNormalizerOutcomeMissingToolInferred = "missing_tool_inferred"
	toolRequestNormalizerOutcomeUnsupportedShape    = "unsupported_shape"
	toolRequestNormalizerOutcomeUnsafeToInfer       = "unsafe_to_infer"
	toolRequestNormalizerOutcomeRequiresTerminal    = "requires_terminal_action"
	toolRequestNormalizerOutcomeTransportMismatch   = "transport_mismatch"

	toolRequestNormalizerTransportNative                  = "native"
	toolRequestNormalizerTransportTextual                 = "textual"
	toolRequestNormalizerTransportFencedJSON              = "fenced_json"
	toolRequestNormalizerTransportKeyValue                = "key_value"
	toolRequestNormalizerTransportKeyValueArgs            = "key_value_args"
	toolRequestNormalizerTransportFunctionCall            = "function_call"
	toolRequestNormalizerTransportNestedArguments         = "nested_arguments"
	toolRequestNormalizerTransportIdiomCLI                = "idiom_cli"
	toolRequestNormalizerTransportXMLToolMarkup           = "xml_tool_markup"
	toolRequestNormalizerTransportStringifiedSubmitRepair = "stringified_submit_plan_repair"
)

type toolRequestSyntaxNormalization struct {
	Request            workspace.ToolRequest
	Raw                string
	OK                 bool
	ParseErr           error
	EventOutcome       string
	EventTransport     string
	ParserFailureClass autonomy.ParserFailureClass
	ActiveToolCall     *llama.ToolCall
	Repaired           bool
	UnsafeExplicitTool bool
}

func runnerAutonomyPolicyInputFromNormalization(n toolRequestSyntaxNormalization) runnerAutonomyPolicyEventInput {
	transport, parserFailure := parserPolicyMetadata(n.Raw, n.ParseErr, n.EventTransport, n.ParserFailureClass)
	input := runnerAutonomyPolicyEventInput{
		EventType:     autonomy.EventToolIntentClassified,
		Transport:     transport,
		ParserFailure: parserFailure,
		RepairSource:  autonomy.RepairSourceNone,
		Result:        strings.TrimSpace(n.EventOutcome),
		Source:        "tooling.intent",
	}
	if n.Repaired {
		input.RepairSource = autonomy.RepairSourceHostNormalizer
	}
	return input
}

func parserPolicyMetadata(raw string, parseErr error, transport string, parserFailureClass autonomy.ParserFailureClass) (autonomy.ToolTransport, autonomy.ParserFailureClass) {
	policyTransport := toolRequestAutonomyTransport(transport)
	parserFailure := autonomy.CanonicalParserFailureClass(parserFailureClass)
	if parserFailure == "" || parserFailure == autonomy.ParserFailureNone {
		parserFailure = toolRequestParserFailureClass(raw, parseErr)
	}
	if (policyTransport == "" || policyTransport == autonomy.ToolTransportUnknown) && parserFailure != "" && parserFailure != autonomy.ParserFailureNone {
		policyTransport = autonomy.ToolTransportTextualCall
	}
	return policyTransport, parserFailure
}

func toolRequestAutonomyTransport(transport string) autonomy.ToolTransport {
	switch strings.TrimSpace(transport) {
	case toolRequestNormalizerTransportNative:
		return autonomy.ToolTransportNativeCall
	case toolRequestNormalizerTransportTextual,
		toolRequestNormalizerTransportFencedJSON,
		toolRequestNormalizerTransportKeyValue,
		toolRequestNormalizerTransportKeyValueArgs,
		toolRequestNormalizerTransportFunctionCall,
		toolRequestNormalizerTransportNestedArguments,
		toolRequestNormalizerTransportIdiomCLI,
		toolRequestNormalizerTransportXMLToolMarkup,
		toolRequestNormalizerTransportStringifiedSubmitRepair:
		return autonomy.ToolTransportTextualCall
	default:
		return autonomy.ToolTransportUnknown
	}
}

func normalizeToolRequestSyntax(message llama.ChatMessage) toolRequestSyntaxNormalization {
	if len(message.ToolCalls) > 0 {
		call := message.ToolCalls[0]
		req, ok, parseErr := parseToolCall(call)

		result := toolRequestSyntaxNormalization{
			Request:        req,
			Raw:            call.Function.Arguments,
			OK:             ok,
			ParseErr:       parseErr,
			ActiveToolCall: &call,
		}
		if parseErr != nil {
			repaired := repairToolRequestSyntax(result)
			if repaired.Repaired {
				return repaired
			}

			result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
			if nativeToolCallTransportMismatch(parseErr) {
				result.EventOutcome = toolRequestNormalizerOutcomeTransportMismatch
			}
			result.EventTransport = toolRequestNormalizerTransportNative
			result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)

			return result
		}

		if repaired := repairToolRequestSyntax(result); repaired.Repaired {
			return repaired
		}

		if ok {
			normalizeParsedToolRequestSyntax(&result)

			result.EventOutcome = toolRequestNormalizerOutcomeNormalized
			result.EventTransport = toolRequestNormalizerTransportNative

			return result
		}

		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportNative

		return result
	}

	req, raw, ok, parseErr := workspace.ParseToolRequest(message.Content)

	result := toolRequestSyntaxNormalization{
		Request:  req,
		Raw:      raw,
		OK:       ok,
		ParseErr: parseErr,
	}
	if parseErr != nil {
		result.OK = false
		repaired := repairToolRequestSyntax(result)
		if repaired.Repaired {
			return repaired
		}

		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportTextual

		if workspace.LooksLikeXMLToolCallMarkup(message.Content) {
			result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
			result.EventTransport = toolRequestNormalizerTransportXMLToolMarkup
			result.UnsafeExplicitTool = unsafeToolShapeHasExplicitTool(message.Content)
		}

		result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)

		return result
	}

	if repaired := repairToolRequestSyntax(result); repaired.Repaired {
		return repaired
	}

	if ok {
		normalizeParsedToolRequestSyntax(&result)

		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = toolRequestNormalizerTransportTextual
		if workspace.LooksLikeXMLToolCallMarkup(message.Content) {
			result.EventTransport = toolRequestNormalizerTransportXMLToolMarkup
		}

		return result
	}

	// Try the unified tokenizer dispatch for non-standard textual formats.
	// This replaces the per-format cascade of normalize* functions.
	if tokenResult := ParseWithAllTokenizers(message.Content); tokenResult.Unsafe {
		result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
		result.EventTransport = textToNormalizerTransport(tokenResult.Transport)
		result.UnsafeExplicitTool = tokenResult.UnsafeExplicitTool

		return result
	} else if tokenResult.OK {
		// Reject write tools inferred from non-native formats to prevent
		// accidental mutation from function-call, CLI, or args-block syntax.
		if tokenizerWriteRequestUnsafe(tokenResult.Transport, tokenResult.Request.Tool) {
			result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
			result.EventTransport = textToNormalizerTransport(tokenResult.Transport)
			result.UnsafeExplicitTool = true

			return result
		}

		result.Request = tokenResult.Request
		result.Raw = tokenResult.Raw
		result.OK = true
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = textToNormalizerTransport(tokenResult.Transport)
		normalizeParsedToolRequestSyntax(&result)

		return result
	}

	if outcome := classifyUnsupportedTextualToolShape(message.Content); outcome != "" {
		result.EventOutcome = outcome
		result.EventTransport = toolRequestNormalizerTransportTextual
	}

	return result
}

func nativeToolCallTransportMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "arguments declared tool")
}

func tokenizerWriteRequestUnsafe(transport string, tool string) bool {
	if !workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	return !(transport == "key_value" && tool == workspace.ToolNameEditFile)
}

func repairToolRequestSyntax(result toolRequestSyntaxNormalization) toolRequestSyntaxNormalization {
	if repairedReq, repairedRaw, repaired := repairNestedArgumentsToolRequest(result.Raw); repaired {
		result.Request = repairedReq
		result.Raw = repairedRaw
		result.OK = true
		result.ParseErr = nil
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = toolRequestNormalizerTransportNestedArguments
		result.ParserFailureClass = autonomy.ParserFailureNone
		result.Repaired = true
		normalizeParsedToolRequestSyntax(&result)

		return result
	}

	repairedReq, repairedRaw, repaired := repairStringifiedSubmitPlanEditsToolRequest(result.Raw, result.ActiveToolCall)
	if !repaired {
		return result
	}

	result.Request = repairedReq
	result.Raw = repairedRaw
	result.OK = true
	result.ParseErr = nil
	result.EventOutcome = toolRequestNormalizerOutcomeNormalized
	result.EventTransport = toolRequestNormalizerTransportStringifiedSubmitRepair
	result.ParserFailureClass = autonomy.ParserFailureNone
	result.Repaired = true
	normalizeParsedToolRequestSyntax(&result)

	return result
}

func repairNestedArgumentsToolRequest(raw string) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return workspace.ToolRequest{}, "", false
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	if len(obj) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	args, ok := obj["arguments"]
	if !ok || !json.Valid(args) {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON(args)
	if err != nil || strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return req, string(args), true
}

func toolRequestParserFailureClass(raw string, parseErr error) autonomy.ParserFailureClass {
	if parseErr == nil {
		return autonomy.ParserFailureNone
	}

	lower := strings.ToLower(parseErr.Error())
	combined := lower + "\n" + strings.ToLower(raw)

	switch {
	case nativeToolCallTransportMismatch(parseErr):
		return autonomy.ParserFailureTransportMismatch
	case strings.Contains(combined, "toolrequest.edits") && strings.Contains(combined, "string"):
		return autonomy.ParserFailureSchemaMismatch
	case strings.Contains(lower, "unexpected end of json") ||
		strings.Contains(lower, "invalid character") ||
		strings.Contains(lower, "syntax error"):
		return autonomy.ParserFailureMalformedJSON
	case strings.Contains(lower, "cannot unmarshal"):
		return autonomy.ParserFailureSchemaMismatch
	case strings.Contains(lower, "missing tool"):
		return autonomy.ParserFailureMissingToolName
	case strings.Contains(lower, "nested request tool") && strings.Contains(lower, "does not match"):
		return autonomy.ParserFailureSchemaMismatch
	default:
		return autonomy.ParserFailureAmbiguousToolCall
	}
}

func repairStringifiedSubmitPlanEditsToolRequest(raw string, activeToolCall *llama.ToolCall) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	toolName := ""
	if activeToolCall != nil {
		toolName = strings.TrimSpace(activeToolCall.Function.Name)
	}

	if toolName == "" {
		_ = json.Unmarshal(obj["tool"], &toolName)
		toolName = strings.TrimSpace(toolName)
	}

	if toolName != workspace.ToolNameSubmitPlan {
		return workspace.ToolRequest{}, "", false
	}

	var encodedEdits string

	if err := json.Unmarshal(obj["edits"], &encodedEdits); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	decodedEdits := strings.TrimSpace(encodedEdits)
	if decodedEdits == "" || !json.Valid([]byte(decodedEdits)) {
		return workspace.ToolRequest{}, "", false
	}

	var edits []planartifact.PlanEdit

	if err := json.Unmarshal([]byte(decodedEdits), &edits); err != nil || len(edits) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	obj["edits"] = json.RawMessage(decodedEdits)
	if _, ok := obj["tool"]; !ok {
		obj["tool"] = json.RawMessage(`"submit_plan"`)
	}

	repaired, err := json.Marshal(obj)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON(repaired)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req.Tool = workspace.ToolNameSubmitPlan

	return req, string(repaired), true
}

func exactFencedJSONPayload(text string) (payload string, ok bool, unsafe bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, false
	}

	if !strings.HasPrefix(trimmed, "```") {
		if strings.Contains(strings.ToLower(trimmed), "```json") {
			return "", false, true
		}

		return "", false, false
	}

	firstLineEnd := strings.Index(trimmed, "\n")
	if firstLineEnd < 0 {
		return "", false, true
	}

	language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed[:firstLineEnd], "```")))
	if language != "json" {
		return "", false, false
	}

	rest := trimmed[firstLineEnd+1:]
	if !strings.HasSuffix(rest, "\n```") {
		return "", false, true
	}

	payload = strings.TrimSpace(strings.TrimSuffix(rest, "\n```"))
	if !strings.HasPrefix(payload, "{") {
		return "", false, true
	}

	return payload, true, false
}

func keyValueArgsToolAndBodyStart(text string) (string, int, bool) {
	tool := ""
	argsStart := -1
	offset := 0

	for _, line := range strings.SplitAfter(text, "\n") {
		rawLine := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(rawLine)
		lower := strings.ToLower(trimmed)

		if tool == "" && trimmed != "" && !strings.HasPrefix(lower, "tool:") {
			return "", 0, false
		}

		if value, ok := strings.CutPrefix(lower, "tool:"); ok {
			original := strings.TrimSpace(rawLine[strings.Index(strings.ToLower(rawLine), "tool:")+len("tool:"):])
			if original == "" || !toolRequestSyntaxIdentifier(original) {
				return "", 0, false
			}

			tool = original
			_ = value
		}

		if strings.HasPrefix(lower, "args:") {
			argsIndex := strings.Index(strings.ToLower(rawLine), "args:")

			argsStart = offset + argsIndex + len("args:")

			break
		}

		offset += len(line)
	}

	if tool == "" || argsStart < 0 {
		return "", 0, false
	}

	return tool, argsStart, true
}

func normalizeKeyValueArgsObject(tool string, object string) (string, bool) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return "", false
	}

	if rawTool, exists := fields["tool"]; exists {
		var explicit string

		if err := json.Unmarshal(rawTool, &explicit); err != nil {
			return "", false
		}

		if explicit = strings.TrimSpace(explicit); explicit != "" && explicit != tool {
			return "", false
		}
	}

	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	payload, err := json.Marshal(fields)
	if err != nil {
		return "", false
	}

	return string(payload), true
}

func normalizeParsedToolRequestSyntax(result *toolRequestSyntaxNormalization) {
	if result == nil || !result.OK {
		return
	}

	if normalizeReadToolAlias(&result.Request) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}

	if normalizeReadFileLinesAlias(result) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}
}

func normalizeReadToolAlias(req *workspace.ToolRequest) bool {
	if req == nil || strings.TrimSpace(req.Tool) != "code_read" || strings.TrimSpace(req.Path) == "" {
		return false
	}

	req.Tool = workspace.ToolNameReadFile

	return true
}

func normalizeReadFileLinesAlias(result *toolRequestSyntaxNormalization) bool {
	if result == nil || strings.TrimSpace(result.Request.Tool) != workspace.ToolNameReadFile || result.Request.StartLine > 0 || result.Request.EndLine > 0 {
		return false
	}

	start, end, ok := readFileLinesAliasFromRaw(result.Raw)
	if !ok {
		return false
	}

	result.Request.StartLine = start
	result.Request.EndLine = end

	return true
}

func readFileLinesAliasFromRaw(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return 0, 0, false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return 0, 0, false
	}

	value, ok := fields["lines"]
	if !ok {
		return 0, 0, false
	}

	var text string

	if err := json.Unmarshal(value, &text); err == nil {
		return parseReadFileLinesAlias(text)
	}

	var number int

	if err := json.Unmarshal(value, &number); err == nil {
		if number <= 0 {
			return 0, 0, false
		}

		return number, number, true
	}

	return 0, 0, false
}

func parseReadFileLinesAlias(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}

	for _, sep := range []string{"-", ":"} {
		if left, right, ok := strings.Cut(raw, sep); ok {
			start, errStart := strconv.Atoi(strings.TrimSpace(left))
			end, errEnd := strconv.Atoi(strings.TrimSpace(right))

			if errStart != nil || errEnd != nil || start <= 0 || end < start {
				return 0, 0, false
			}

			return start, end, true
		}
	}

	line, err := strconv.Atoi(raw)
	if err != nil || line <= 0 {
		return 0, 0, false
	}

	return line, line, true
}

func parseFunctionCallToolArguments(args string) (map[string]string, bool) {
	values := map[string]string{}
	if strings.TrimSpace(args) == "" {
		return values, true
	}

	for _, part := range splitFunctionCallArguments(args) {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, false
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if !keyValueToolRequestFieldAllowed(key) {
			return nil, false
		}

		decoded, ok := decodeFunctionCallArgumentValue(value)
		if !ok || decoded == "" {
			return nil, false
		}

		values[key] = decoded
	}

	return values, true
}

func splitFunctionCallArguments(args string) []string {
	var (
		parts []string
		b     strings.Builder
	)

	quote := rune(0)
	escaped := false

	for _, r := range args {
		if escaped {
			b.WriteRune(r)

			escaped = false

			continue
		}

		if r == '\\' && quote != 0 {
			b.WriteRune(r)

			escaped = true

			continue
		}

		if quote != 0 {
			b.WriteRune(r)

			if r == quote {
				quote = 0
			}

			continue
		}

		if r == '"' || r == '\'' {
			quote = r
			b.WriteRune(r)

			continue
		}

		if r == ',' {
			parts = append(parts, b.String())
			b.Reset()

			continue
		}

		b.WriteRune(r)
	}

	parts = append(parts, b.String())

	return parts
}

func decodeFunctionCallArgumentValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	if strings.HasPrefix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", false
		}

		return strings.TrimSpace(decoded), true
	}

	if strings.HasPrefix(value, `'`) {
		if !strings.HasSuffix(value, `'`) || len(value) < 2 {
			return "", false
		}

		return strings.TrimSpace(strings.Trim(value, `'`)), true
	}

	if strings.ContainsAny(value, " \t") {
		return "", false
	}

	return strings.Trim(value, `"'`), true
}

func toolRequestSyntaxIdentifier(value string) bool {
	if value == "" {
		return false
	}

	for i, r := range value {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}

		return false
	}

	return true
}

func toolRequestFromSyntaxValues(tool string, values map[string]string) (workspace.ToolRequest, bool) {
	req := workspace.ToolRequest{
		Tool:            tool,
		ID:              values["id"],
		Key:             values["key"],
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Capability:      values["capability"],
		Mode:            values["mode"],
		Task:            values["task"],
		Glob:            values["glob"],
		Language:        values["language"],
		Resolution:      values["resolution"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
		Purpose:         values["purpose"],
		Command:         values["command"],
		Description:     values["description"],
		Content:         values["content"],
		Prompt:          values["prompt"],
		Agent:           values["agent"],
		Workdir:         values["workdir"],
		Handle:          values["handle"],
		Root:            values["root"],
		Destination:     values["destination"],
		Status:          values["status"],
		PacketPath:      values["packet_path"],
		OutputPath:      values["output_path"],
		Component:       values["component"],
		Category:        values["category"],
		SessionID:       values["session_id"],
		Confidence:      values["confidence"],
		Severity:        values["severity"],
		SuggestedFix:    values["suggested_fix"],
		Expected:        values["expected"],
		Observed:        values["observed"],
		Workaround:      values["workaround"],
		Impact:          values["impact"],
		TaskID:          values["task_id"],
		OldText:         values["old_text"],
		NewText:         values["new_text"],
	}

	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(values["scope"]) != "" {
		req.Path = values["scope"]
	}

	// Parse read_file lines alias: "lines: N-M" becomes start_line=N,
	// end_line=M; "lines: N" becomes start_line=N, end_line=N.
	if raw := strings.TrimSpace(values["lines"]); strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
		strings.TrimSpace(values["start_line"]) == "" && strings.TrimSpace(values["end_line"]) == "" && raw != "" {

		start, end, ok := parseReadFileLinesAlias(raw)
		if !ok {
			return workspace.ToolRequest{}, false
		}

		req.StartLine = start
		req.EndLine = end
	}

	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
		"max_depth":     func(v int) { req.MaxDepth = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}

			assign(v)
		}
	}

	for key, assign := range map[string]func(bool){
		"include_evidence": func(v bool) { req.IncludeEvidence = v },
		"include_source":   func(v bool) { req.IncludeSource = v },
		"include_files":    func(v bool) { req.IncludeFiles = v },
		"refresh":          func(v bool) { req.Refresh = v },
		"replace_all":      func(v bool) { req.ReplaceAll = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}

			assign(v)
		}
	}

	return req, true
}

func keyValueToolRequestFieldAllowed(key string) bool {
	switch key {
	case "tool", "id", "key", "path", "file", "scope", "pattern", "query", "capability", "mode", "task", "glob", "language", "resolution",
		"named_gap", "why_not_answering", "start_line", "end_line", "limit", "budget_tokens", "max_depth",
		"include_evidence", "include_source", "include_files", "refresh", "lines", "replace_all",
		"purpose", "command", "description", "content", "prompt", "agent", "workdir", "handle", "root", "destination", "status",
		"packet_path", "output_path", "component", "category", "session_id", "confidence", "severity", "suggested_fix",
		"expected", "observed", "workaround", "impact", "task_id", "recursive", "old_text", "new_text", "new_text_ref", "content_ref":
		return true
	default:
		return false
	}
}

func classifyUnsupportedTextualToolShape(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "```json") || strings.Contains(trimmed, "{") {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}

	if strings.Contains(trimmed, ":") && keyValueLikeText(trimmed) {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}

	return ""
}

func keyValueLikeText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		if keyValueToolRequestFieldAllowed(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}

	return false
}

func unsafeTextualWriteAttempt(text string) (tool string, transport string, ok bool) {
	normalization := normalizeToolRequestSyntax(llama.ChatMessage{Content: text})
	if normalization.OK ||
		normalization.ParseErr != nil ||
		normalization.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer {

		return "", "", false
	}

	tool = unsafeTextualToolName(text)
	if !workspace.IsWorkspaceWriteTool(tool) {
		return "", "", false
	}

	return tool, strings.TrimSpace(normalization.EventTransport), true
}

func unsafeTextualToolName(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, body, ok := strings.Cut(line, ":")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
			return ""
		}

		body = strings.TrimSpace(body)
		if left, _, ok := strings.Cut(body, "("); ok {
			body = strings.TrimSpace(left)
		}

		if left, _, ok := strings.Cut(body, " "); ok {
			body = strings.TrimSpace(left)
		}

		return strings.ToLower(strings.TrimSpace(body))
	}

	return ""
}

func unsafeToolShapeHasExplicitTool(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	return unsafeTextualToolName(text) != "" || jsonObjectHasToolField(text)
}

func jsonObjectHasToolField(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &obj); err == nil {
		_, ok := obj["tool"]
		return ok
	}

	lower := strings.ToLower(text)
	return strings.Contains(lower, `"tool"`) || strings.Contains(lower, `'tool'`)
}

// ToolRequestTokenizer converts raw model output into a tool name and
// key-value field map.
type ToolRequestTokenizer interface {
	Tokenize(raw string) ToolRequestTokenization
}

type ToolRequestTokenization struct {
	Tool               string
	Fields             map[string]string
	OK                 bool
	Unsafe             bool
	UnsafeExplicitTool bool
}

// ParseResult is the unified output of all tokenizers after normalization.
type ParseResult struct {
	Request            workspace.ToolRequest
	Raw                string
	OK                 bool
	Unsafe             bool
	UnsafeExplicitTool bool
	// Transport identifies the text format that was parsed, for telemetry.
	Transport string
}

// ParseToolRequestFromTokenizer runs a tokenizer and converts its output
// through the shared toolRequestFromSyntaxValues function. This is the
// single convergence point for all parsing strategies.
func ParseToolRequestFromTokenizer(t ToolRequestTokenizer, raw string) ParseResult {
	tokens := t.Tokenize(raw)
	if tokens.Unsafe {
		return ParseResult{Unsafe: true, UnsafeExplicitTool: tokens.UnsafeExplicitTool}
	}

	if !tokens.OK {
		return ParseResult{}
	}

	req, ok := toolRequestFromSyntaxValues(tokens.Tool, tokens.Fields)
	if !ok {
		return ParseResult{Unsafe: true, UnsafeExplicitTool: strings.TrimSpace(tokens.Tool) != ""}
	}

	return ParseResult{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		OK:      true,
	}
}

// ParseWithAllTokenizers tries each registered tokenizer in priority order
// and returns the first successful parse. Priority order matches the legacy
// normalizeToolRequestSyntax cascade.
func ParseWithAllTokenizers(raw string) ParseResult {
	type namedTokenizer struct {
		name      string
		tokenizer ToolRequestTokenizer
	}

	tokenizers := []namedTokenizer{
		{"nested_arguments", nestedArgumentsToolRequestTokenizer{}},
		{"fenced_json", fencedJSONToolRequestTokenizer{}},
		{"idiom_cli", idiomCLIToolRequestTokenizer{}},
		{"key_value_args", keyValueArgsToolRequestTokenizer{}},
		{"function_call", functionCallToolRequestTokenizer{}},
		{"key_value", keyValueToolRequestTokenizer{}},
	}

	for _, nt := range tokenizers {
		result := ParseToolRequestFromTokenizer(nt.tokenizer, raw)
		if result.Unsafe {
			return ParseResult{Unsafe: true, Transport: nt.name, UnsafeExplicitTool: result.UnsafeExplicitTool}
		}

		if result.OK {
			result.Transport = nt.name

			return result
		}
	}

	return ParseResult{}
}

type keyValueToolRequestTokenizer struct{}

func (keyValueToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ToolRequestTokenization{}
	}

	lines := strings.Split(trimmed, "\n")
	toolLineIdx := -1
	proseLines := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if ok && textutil.EqualTrimFold(key, "tool") {
			toolLineIdx = i
			break
		}

		proseLines++
		if proseLines > 5 {
			return ToolRequestTokenization{Unsafe: true}
		}
	}

	if toolLineIdx < 0 {
		if keyValueNoToolUnsafe(lines) {
			return ToolRequestTokenization{Unsafe: true}
		}

		return ToolRequestTokenization{}
	}

	values := map[string]string{}
	explicitToolSeen := false
	sawKeyValue := false
	lastKey := ""
	continuationLines := 0
	appendContinuation := func(line string) bool {
		if !keyValueContinuationField(lastKey) {
			return true
		}

		continuationLines++
		if continuationLines > 8 || len(values[lastKey])+len(line)+1 > 8192 {
			return false
		}

		values[lastKey] += "\n" + line

		return true
	}

	for _, rawLine := range lines[toolLineIdx:] {
		line := strings.TrimRight(rawLine, "\r")
		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" {
			if sawKeyValue && keyValueBlankContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			if sawKeyValue && strings.TrimSpace(values["tool"]) != "" {
				break
			}

			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			if sawKeyValue {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			return ToolRequestTokenization{Unsafe: true}
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if sawKeyValue && keyValueRawValueField(lastKey) && !keyValueRawFieldStructuralTransition(lastKey, key) {
			if !appendContinuation(line) {
				return ToolRequestTokenization{Unsafe: true}
			}

			continue
		}

		if keyValueRawValueField(key) {
			value = normalizeRawKeyValueField(value)
		} else {
			value = trimMatchingSyntaxQuotes(strings.TrimSpace(value))
		}

		if key == "" || (value == "" && !keyValueAllowsEmptyValue(key, values)) {
			return ToolRequestTokenization{Unsafe: true}
		}

		if key == "tool" && strings.TrimSpace(values["tool"]) != "" {
			break
		}

		if !keyValueToolRequestFieldAllowed(key) {
			if sawKeyValue && keyValueContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			return ToolRequestTokenization{Unsafe: true}
		}

		sawKeyValue = true
		continuationLines = 0
		values[key] = value
		if key == "tool" {
			explicitToolSeen = true
		}
		lastKey = key
	}

	if !sawKeyValue {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: explicitToolSeen}
	}

	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: explicitToolSeen}
	}

	return ToolRequestTokenization{Tool: tool, Fields: values, OK: true}
}

func keyValueNoToolUnsafe(lines []string) bool {
	for _, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}

		if keyValueMissingToolActionField(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}

	return false
}

func keyValueMissingToolActionField(key string) bool {
	switch key {
	case "path", "file", "scope", "pattern", "query", "symbol", "capability", "operation", "action", "kind",
		"mode", "task", "glob", "language", "resolution", "named_gap", "why_not_answering", "start_line",
		"end_line", "limit", "budget_tokens", "max_depth", "include_evidence", "include_source", "include_files",
		"refresh", "lines", "replace_all", "purpose", "command", "content", "prompt", "agent", "workdir",
		"handle", "root", "destination", "packet_path", "output_path", "component", "category", "session_id",
		"task_id", "recursive", "old_text", "new_text", "new_text_ref", "content_ref":
		return true
	default:
		return false
	}
}

func normalizeRawKeyValueField(value string) string {
	if strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") {
		value = value[1:]
	}

	return trimMatchingSyntaxQuotes(value)
}

func trimMatchingSyntaxQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	first := value[0]
	last := value[len(value)-1]

	if (first == '"' || first == '\'') && first == last {
		return value[1 : len(value)-1]
	}

	return value
}

func keyValueRawValueField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueBlankContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueRawFieldStructuralTransition(lastKey, key string) bool {
	switch strings.ToLower(strings.TrimSpace(lastKey)) {
	case "old_text":
		return key == "new_text"
	case "new_text":
		switch key {
		case "description", "task_id", "replace_all":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func keyValueAllowsEmptyValue(key string, values map[string]string) bool {
	return key == "new_text" && strings.TrimSpace(values["tool"]) == workspace.ToolNameEditFile
}

func keyValueContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "prompt", "content", "old_text", "new_text", "description", "purpose", "task", "query", "named_gap", "why_not_answering", "expected", "observed", "workaround", "impact", "suggested_fix":
		return true
	default:
		return false
	}
}

// fencedJSONToolRequestTokenizer handles model output wrapped in a fenced
// JSON code block:
//
//	```json
//	{"tool": "read_file", "path": "some/path", "limit": 50}
//	```
type fencedJSONToolRequestTokenizer struct{}

func (fencedJSONToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	payload, ok, unsafe := exactFencedJSONPayload(text)
	if !ok {
		if unsafe {
			return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: jsonObjectHasToolField(payload)}
		}

		return ToolRequestTokenization{}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: jsonObjectHasToolField(payload)}
	}

	toolRaw, ok := obj["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var tool string

	if err := json.Unmarshal(toolRaw, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			// If it's not a string, try marshaling back to JSON string.
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// functionCallToolRequestTokenizer handles single-line function call syntax:
//
//	tool: read_file(path="some/path", limit=50)
type functionCallToolRequestTokenizer struct{}

func (functionCallToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ToolRequestTokenization{}
	}

	// Must be single-line to be a function call.
	if strings.Contains(trimmed, "\n") {
		return ToolRequestTokenization{}
	}

	if strings.Contains(trimmed, "{") || strings.Contains(trimmed, "```") {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: strings.HasPrefix(strings.ToLower(trimmed), "tool:")}
	}

	key, body, ok := strings.Cut(trimmed, ":")
	if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
		return ToolRequestTokenization{}
	}

	body = strings.TrimSpace(body)

	open := strings.Index(body, "(")

	if open < 0 {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	if !strings.HasSuffix(body, ")") {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	tool := strings.TrimSpace(body[:open])
	if !toolRequestSyntaxIdentifier(tool) {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	args := strings.TrimSpace(body[open+1 : len(body)-1])
	values, ok := parseFunctionCallToolArguments(args)
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	return ToolRequestTokenization{Tool: tool, Fields: values, OK: true}
}

// idiomCLIToolRequestTokenizer handles CLI-style invocation:
//
//	idiom tool read_file {"path": "some/path"}
type idiomCLIToolRequestTokenizer struct{}

func (idiomCLIToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	if !strings.HasPrefix(lower, "idiom tool ") {
		return ToolRequestTokenization{}
	}

	objectStart := strings.Index(trimmed, "{")
	if objectStart < 0 {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	prefix := strings.TrimSpace(trimmed[:objectStart])
	parts := strings.Fields(prefix)

	if len(parts) < 3 || !strings.EqualFold(parts[0], "idiom") || !strings.EqualFold(parts[1], "tool") || !toolRequestSyntaxIdentifier(parts[2]) {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true}
	}

	tool := parts[2]

	// If JSON specifies a different tool, it must match.
	if toolRaw, ok := obj["tool"]; ok {
		var jsonTool string

		if err := json.Unmarshal(toolRaw, &jsonTool); err == nil && strings.TrimSpace(jsonTool) != "" {
			if !strings.EqualFold(tool, jsonTool) {
				return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
			}
		}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// keyValueArgsToolRequestTokenizer handles args-block invocation:
//
//	tool: name
//	args: {"key": "value", ...}
type keyValueArgsToolRequestTokenizer struct{}

func (keyValueArgsToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(trimmed, "{") || !strings.Contains(strings.ToLower(trimmed), "args:") {
		return ToolRequestTokenization{}
	}

	tool, argsStart, ok := keyValueArgsToolAndBodyStart(trimmed)
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: unsafeToolShapeHasExplicitTool(trimmed)}
	}

	objectOffset := strings.Index(trimmed[argsStart:], "{")
	if objectOffset < 0 {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	objectStart := argsStart + objectOffset
	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	payload, ok := normalizeKeyValueArgsObject(tool, object)
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	toolRaw, ok := obj["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	var jsonTool string

	if err := json.Unmarshal(toolRaw, &jsonTool); err != nil || strings.TrimSpace(jsonTool) == "" {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: jsonTool, Fields: fields, OK: true}
}

// nestedArgumentsToolRequestTokenizer handles a JSON wrapper with the payload
// inside an "arguments" field:
//
//	{"arguments": {"tool": "read_file", "path": "some/path", "limit": 50}}
//
// The outer object must have exactly one key ("arguments") whose value is a
// valid JSON ToolRequest.
type nestedArgumentsToolRequestTokenizer struct{}

func (nestedArgumentsToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	raw := strings.TrimSpace(text)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return ToolRequestTokenization{}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ToolRequestTokenization{}
	}

	if len(obj) != 1 {
		return ToolRequestTokenization{}
	}

	args, ok := obj["arguments"]
	if !ok || !json.Valid(args) {
		return ToolRequestTokenization{}
	}

	var inner map[string]json.RawMessage

	if err := json.Unmarshal(args, &inner); err != nil {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	toolRaw, ok := inner["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var tool string

	if err := json.Unmarshal(toolRaw, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return ToolRequestTokenization{Unsafe: true, UnsafeExplicitTool: true}
	}

	fields := make(map[string]string, len(inner))

	for key, raw := range inner {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// textToNormalizerTransport maps the tokenizer's transport identifier to the
// corresponding normalizer transport constant for telemetry parity.
func textToNormalizerTransport(transport string) string {
	switch transport {
	case "nested_arguments":
		return toolRequestNormalizerTransportNestedArguments
	case "fenced_json":
		return toolRequestNormalizerTransportFencedJSON
	case "idiom_cli":
		return toolRequestNormalizerTransportIdiomCLI
	case "key_value_args":
		return toolRequestNormalizerTransportKeyValueArgs
	case "function_call":
		return toolRequestNormalizerTransportFunctionCall
	case "key_value":
		return toolRequestNormalizerTransportKeyValue
	default:
		return toolRequestNormalizerTransportTextual
	}
}

// buildLlamaTools converts workspace tool definitions into the shape the
// llama client may send to the server — i.e. OpenAI function-calling specs.
// Some backends intentionally downgrade to idiom's JSON tool protocol at the
// llama client boundary when their native parser cannot be trusted to return
// structured errors.
// Called once per turn when the runner sets up ChatOptions. Native-capable
// backends such as llama.cpp can route the model's tool_calls through the
// OpenAI channel; downgraded backends still receive the same tool manifest in
// the runtime brief for idiom-side JSON parsing.
func buildLlamaTools(defs []workspace.ToolDefinition) []llama.Tool {
	if len(defs) == 0 {
		return nil
	}

	out := make([]llama.Tool, 0, len(defs))
	for _, def := range defs {
		params := json.RawMessage(def.ParametersJSON)
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		out = append(out, llama.Tool{
			Type: "function",
			Function: llama.ToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  params,
			},
		})
	}

	return out
}

// postReadinessRequiredFields lists the JSON Schema properties required on
// read-class source-refresh calls only when the consolidated runner contract
// selects an explicit mutation/source-refresh surface. Answerable read-only
// evidence contracts to a final-answer surface instead.
var postReadinessRequiredFields = []string{"named_gap", "why_not_answering"}

// applyPostReadinessSchemaRequirements rewrites the JSON Schema of every
// read-class tool in tools to mark `named_gap` and `why_not_answering` as
// required and to ensure both properties are declared. Generic across
// tools — anything that is_read_class returns true for picks up the same
// schema constraint. Non-read-class tools are returned unchanged.
//
// For `read_file`, also require a concrete path and line range. After
// readiness, an unanchored or path-wide file read is nearly always an
// inefficient substitute for either answering or choosing a symbol/range probe,
// so push that choice into the tool contract instead of spending a model turn
// on a runtime rejection.
//
// Called from the runner's rebuildTools only when
// postReadinessToolContractForBrief returns RequireSourceRefreshContract.
// Idempotent: calling twice in a row produces the same schema.
func applyPostReadinessSchemaRequirements(tools []llama.Tool) []llama.Tool {
	if len(tools) == 0 {
		return tools
	}

	out := make([]llama.Tool, len(tools))
	for i, tool := range tools {
		if !workspace.IsReadClassToolName(tool.Function.Name) {
			out[i] = tool
			continue
		}

		mutated, ok := mutateSchemaRequireFields(tool.Function.Parameters, postReadinessRequiredFields)
		if !ok {
			out[i] = tool
			continue
		}

		if tool.Function.Name == workspace.ToolNameReadFile {
			if narrowed, narrowOK := mutateSchemaRequireLineRange(mutated); narrowOK {
				mutated = narrowed
			}
		} else if isPostReadinessLineRangeTool(tool.Function.Name) {
			if exposed, exposeOK := mutateSchemaExposeLineRange(mutated); exposeOK {
				mutated = exposed
			}
		}

		tool.Function.Parameters = mutated
		out[i] = tool
	}

	return out
}

// stripPostReadinessBroadTools removes tools that are inappropriate under an
// explicit mutation/source-refresh contract. Broad file discovery
// (search_files, list_files) encourages rediscovery instead of resolving the
// scoped write contract. Answerable read-only evidence uses a separate
// final-answer surface instead of schema-constrained source refresh.
func stripPostReadinessBroadTools(tools []llama.Tool) []llama.Tool {
	if len(tools) == 0 {
		return tools
	}

	out := make([]llama.Tool, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Function.Name)
		if name == workspace.ToolNameSearchFiles || name == workspace.ToolNameListFiles {
			continue
		}

		out = append(out, t)
	}

	return out
}

func mutateSchemaExposeLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "optional 1-indexed line number for an exact follow-up range after readiness",
			}
		}

		entry["minimum"] = 1
		props[name] = entry
	}

	schema["properties"] = props

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

func mutateSchemaRequireLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	if _, ok := props["path"]; !ok {
		props["path"] = map[string]any{
			"type":        "string",
			"description": "repository-relative file path; required after readiness so read_file has a concrete target",
		}
	}

	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "1-indexed line number; required after readiness so read_file remains a narrow probe",
			}
		}

		entry["minimum"] = 1
		props[name] = entry
	}

	schema["properties"] = props

	requiredSet := make(map[string]struct{})

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}

	for _, name := range []string{"path", "start_line", "end_line"} {
		requiredSet[name] = struct{}{}
	}

	required := make([]string, 0, len(requiredSet))

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}

	for _, name := range []string{"path", "start_line", "end_line"} {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}

	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

func isPostReadinessLineRangeTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, "_read_symbol") || strings.HasSuffix(name, "_extract_body")
}

// mutateSchemaRequireFields takes a raw JSON Schema (object) and adds the
// given names to its `required` array and `properties` map. Returns the
// new raw schema and ok=true. Returns ok=false when the input is not an
// object schema we can sensibly extend.
func mutateSchemaRequireFields(raw json.RawMessage, fields []string) (json.RawMessage, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{}}`)
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	if t, _ := schema["type"].(string); t != "" && t != "object" {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	for _, name := range fields {
		if _, exists := props[name]; !exists || props[name].(map[string]any)["minLength"] == nil {
			props[name] = map[string]any{
				"type":        "string",
				"minLength":   1,
				"description": postReadinessFieldDescription(name),
			}
		}
	}

	schema["properties"] = props

	requiredSet := make(map[string]struct{})

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}

	for _, name := range fields {
		requiredSet[name] = struct{}{}
	}

	required := make([]string, 0, len(requiredSet))

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}

	for _, name := range fields {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}

	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

func postReadinessFieldDescription(name string) string {
	switch name {
	case "named_gap":
		return "after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; reference a specific symbol, file path, or narrow line range"
	case "why_not_answering":
		return "after answer-readiness or a child packet, state what would be materially wrong or missing if you answered now"
	default:
		return ""
	}
}

// toolResultMessage builds the message we append to turnMessages after
// a tool executes. When the call came in via native tool_calls (call is
// non-nil), we return a role:"tool" message linked by tool_call_id:
// OpenAI's documented shape, which Qwen was trained on. The main runner
// synthesizes a tool_call for legacy inline-JSON tool requests before
// recording the result, so file/tool output stays in the tool channel
// rather than becoming system-strength prompt text. The nil-call fallback
// remains only for low-level tests or non-runner callers.
func toolResultMessage(call *llama.ToolCall, result workspace.ToolResult) llama.ChatMessage {
	if call != nil {
		id := call.ID
		if id == "" {
			// DeepSeek and other strict OpenAI-protocol backends require
			// a tool message with tool_call_id after every tool_calls
			// message. Generate a synthetic ID when the call's ID is
			// empty (e.g., when the harness injects a rejected-result
			// message for a textual-tool backend that was normalized to
			// a native-tool history shape).
			id = fmt.Sprintf("rejected_%s_%d", call.Function.Name, time.Now().UnixNano())
		}

		return llama.ChatMessage{
			Role:       "tool",
			ToolCallID: id,
			Name:       call.Function.Name,
			Content:    workspace.FormatToolResultForModel(result),
		}
	}

	return llama.ChatMessage{
		Role:    "system",
		Content: workspace.FormatToolResultForModel(result),
	}
}

// toolResultTouchedPaths returns the set of workspace paths a tool call
// affected (or tried to read), so the InstructionAttacher can walk up
// from those directories and surface nearby configured instruction files once.
// Kept intentionally shallow: we only care about tools that name a
// single file or a patch envelope.
func toolResultTouchedPaths(req workspace.ToolRequest, _ workspace.ToolResult) []string {
	if workspace.ToolRequestPathAttachesInstructions(req.Tool) {
		if req.Path != "" {
			return []string{req.Path}
		}
	}

	switch req.Tool {
	case workspace.ToolNameApplyPatch:
		ops, err := patching.Parse(req.Content)
		if err != nil {
			return nil
		}

		return patching.OpPaths(ops)
	}

	return nil
}

// parseToolCall unmarshals the arguments JSON the model produced in a
// ToolCall into a typed ToolRequest. The function name is the tool;
// for built-in tools the arguments JSON decodes directly into
// ToolRequest's field set (they overlap by design). For
// `mcp:<server>/<tool>` calls the raw JSON is preserved verbatim in
// ToolRequest.Args — MCP tools have arbitrary JSON-Schema parameters
// that do not map onto this struct's typed fields, and forcing them
// through the typed unmarshaller would silently drop fields. Returns
// ok=false when the call has no function name.
func parseToolCall(call llama.ToolCall) (workspace.ToolRequest, bool, error) {
	if call.Function.Name == "" {
		return workspace.ToolRequest{}, false, nil
	}

	req := workspace.ToolRequest{Tool: call.Function.Name}
	if strings.HasPrefix(call.Function.Name, "mcp:") {
		if call.Function.Arguments != "" {
			req.Args = json.RawMessage(call.Function.Arguments)
		} else {
			req.Args = json.RawMessage(`{}`)
		}

		return req, true, nil
	}

	if call.Function.Arguments != "" {
		decoded, err := workspace.DecodeToolRequestJSON([]byte(call.Function.Arguments))
		if err != nil {
			return workspace.ToolRequest{}, false, err
		}

		req = decoded
		req.Args = json.RawMessage(call.Function.Arguments)
	} else {
		req.Args = json.RawMessage(`{}`)
	}

	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = call.Function.Name
	} else if strings.TrimSpace(req.Tool) != call.Function.Name {
		return workspace.ToolRequest{}, false, fmt.Errorf("native tool call %q arguments declared tool %q", call.Function.Name, req.Tool)
	}

	return req, true, nil
}

// NormalizeRunnerPath cleans a workspace-relative path for consistent comparison.
func NormalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func runnerPathEscapesWorkspace(raw string) bool {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, "`'\"()[]{}<>,.;")
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path) {
		return true
	}

	path = filepath.ToSlash(strings.ReplaceAll(path, `\`, `/`))
	return path == ".." || strings.HasPrefix(path, "../")
}

// ContainsAny reports whether text contains any of the given needles.
func ContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Internal helpers extracted from runner_plan_then_execute_guard.go
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Objective text helpers
// ---------------------------------------------------------------------------

// ReadOnlyObjectiveSupportTerm normalizes a term for comparison (lowercase,
// singular).
func ReadOnlyObjectiveSupportTerm(term string) string {
	term = strings.TrimSpace(strings.ToLower(term))
	if len(term) > 3 && strings.HasSuffix(term, "s") {
		term = strings.TrimSuffix(term, "s")
	}
	return term
}

// SingularObjectiveWord trims a trailing 's' from a word to produce a
// singular form, unless the word ends with "ss".
func SingularObjectiveWord(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return strings.TrimSuffix(word, "s")
	}
	return word
}

// RecoverySearchStopWords returns the set of stop words used when extracting
// search terms from an objective.
func RecoverySearchStopWords() map[string]struct{} {
	return map[string]struct{}{
		"actual": {}, "please": {}, "replace": {}, "with": {}, "from": {}, "that": {}, "this": {}, "the": {}, "and": {}, "for": {}, "you": {}, "can": {}, "top": {}, "right": {}, "left": {}, "path": {}, "alias": {},
		"continuing": {}, "continue": {}, "completion": {}, "complete": {}, "work": {}, "docs": {}, "document": {}, "design": {}, "implement": {}, "implementation": {}, "bounded": {}, "topic": {}, "only": {}, "must": {},
		"against": {}, "existing": {}, "focus": {}, "focused": {}, "add": {}, "regression": {}, "tests": {}, "test": {}, "proving": {}, "without": {}, "partial": {}, "durable": {}, "mutation": {},
		"repo": {}, "repository": {}, "discovery": {}, "first": {}, "make": {}, "minimal": {}, "code": {}, "changes": {}, "change": {}, "requested": {}, "request": {},
		"tdd": {}, "prd": {}, "adr": {}, "brief": {}, "research": {}, "workflow": {}, "workflows": {}, "markdown": {},
		// "model" intentionally kept — it carries domain signal for path→objective matching.
		"send": {}, "through": {}, "plan": {}, "execute": {}, "expose": {}, "read": {}, "external": {}, "agent": {}, "agents": {},
	}
}

// RecoveryObjectiveSearchWords extracts content-bearing search terms from an
// objective string, filtered by stop words and minimum length.
func RecoveryObjectiveSearchWords(objective string, max int) []string {
	if max <= 0 {
		max = 8
	}

	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return nil
	}

	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")

	lower = replacer.Replace(lower)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := RecoverySearchStopWords()
	terms := make([]string, 0, max)
	seen := make(map[string]struct{})

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if _, skip := stop[word]; skip {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		terms = append(terms, word)

		if len(terms) >= max {
			break
		}
	}

	return terms
}

// CandidateAnchorOnlyText reports whether the given text contains only anchor
// metadata with no source body content.
func CandidateAnchorOnlyText(text string) bool {
	lower := strings.ToLower(text)

	return strings.Contains(lower, "path-filter miss") &&
		strings.Contains(lower, "candidate_anchor_status: anchors only")
}

// ObjectiveAsksImplementationShape reports whether the objective asks about
// implementation shape ("what would it take", "what needs to change", etc.)
func ObjectiveAsksImplementationShape(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return ContainsAny(lower,
		" what would it take ",
		" what needs to change ",
		" what would need to change ",
		" how would ",
		" how should ",
	)
}

// IsDiagnosticLogArtifactPath reports whether the path points to a known
// diagnostic log artifact that should be excluded from source evidence.
func IsDiagnosticLogArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case lower == "logs" || strings.HasPrefix(lower, "logs/"):
		return true
	case lower == ".idiom/logs" || strings.HasPrefix(lower, ".idiom/logs/"):
		return true
	case lower == ".idiom/comprehension/traces" || strings.HasPrefix(lower, ".idiom/comprehension/traces/"):
		return true
	case lower == "traces" || strings.HasPrefix(lower, "traces/"):
		return true
	case strings.Contains(lower, "/.idiom/logs/") || strings.Contains(lower, "/.idiom/comprehension/traces/"):
		return true
	default:
		return false
	}
}

// IsNonSourceEvidenceArtifactPath reports whether the path points to a
// non-source artifact that should be excluded from source evidence.
func IsNonSourceEvidenceArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case IsDiagnosticLogArtifactPath(lower):
		return true
	case lower == "transcripts" || strings.HasPrefix(lower, "transcripts/"):
		return true
	case lower == ".idiom" || strings.HasPrefix(lower, ".idiom/"):
		return true
	case lower == "idiom" || strings.HasPrefix(lower, "idiom/"):
		return true
	case lower == ".codex/discovery" || strings.HasPrefix(lower, ".codex/discovery/"):
		return true
	case lower == "testdata/swe/generated-logs" || strings.HasPrefix(lower, "testdata/swe/generated-logs/"):
		return true
	default:
		return false
	}
}

type Intent struct {
	ToolName    string
	ActionClass string
	Capability  string
	Path        string
	Confidence  string
	Transport   string
	ParserState string
}
