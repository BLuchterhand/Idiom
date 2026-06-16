package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	Handle string
	Path   string
	Start  int
	End    int
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

func debugLogPhaseScopeRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, sessionID string) (string, string, bool) {
	if !debugLogContractApplies(brief, results) || debugLogEvidenceSatisfied(brief, results) {
		return "", "", false
	}

	selection, ok := latestDebugLogSelection(results)
	if !ok || !hasDebugLogReadForSelection(results, selection) {
		return "", "", false
	}

	if path, ok := concreteDebugLogArtifactPathForRequest(req, results); ok &&
		!debugLogSelectionMatchesPath(selection, path) &&
		!currentSessionDebugLogPath(path, sessionID) {

		return "", "", false
	}

	if isReadClassTool(req) && !debugLogRequestTargetsSelection(req, selection) {
		return "Selected-log scope enforced",
			"you already selected and read the newest relevant log. Stay on that log until you can cite the weird decision or declare the missing datum.",
			true
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !debugLogRequestTargetsSelection(req, selection) {
		return "", "", false
	}

	if _, ok := latestDebugLogSearchHit(results); ok {
		return "", "", false
	}

	if debugLogReadRequestAddsWindow(req, results, selection) {
		return "", "", false
	}

	return "Blind log reread rejected",
		"you already have the newest tail of the selected log. A final answer is available if that tail supports it; otherwise search within the selected log for one concrete marker.",
		true
}

func debugLogFollowupRequest(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !debugLogContractApplies(brief, results) {
		return workspace.ToolRequest{}, false
	}

	hit, ok := latestDebugLogSearchHit(results)
	if !ok || hasDebugLogReadWindowForHit(results, hit) {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchContent:
		return debugLogReadWindowRequest(hit), true
	case workspace.ToolNameReadFile:
		if debugLogRequestTargetsHit(req, hit) && debugLogRequestCoversHit(req, hit) {
			return workspace.ToolRequest{}, false
		}

		return debugLogReadWindowRequest(hit), true
	default:
		return workspace.ToolRequest{}, false
	}
}

func latestDebugLogSelection(results []workspace.ToolResult) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !isStrictDebugLogArtifactPath(window.Path) {
				continue
			}

			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) {
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
		if path == "" || !isStrictDebugLogArtifactPath(path) {
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
			if !ok || !isStrictDebugLogArtifactPath(window.Path) || currentSessionDebugLogPath(window.Path, sessionID) {
				continue
			}

			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) || currentSessionDebugLogPath(hit.Path, sessionID) {
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

		if selection.Handle != "" && window.Handle == selection.Handle {
			return true
		}

		if selection.Path != "" && window.Path == selection.Path {
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
	if strings.TrimSpace(brief.TaskFamily) == "debug_logs" {
		return true
	}

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

	if !containsAny(words, " if available", " if present", " when available", " where available") {
		return false
	}

	return containsAny(words,
		" source ", " source files ", " code ", " bug ", " behavior bug ", " rca ",
		" tests ", " fix shape ", " owning boundary ", " implementation ",
	)
}

func objectiveRequestsSourceFeatureInspection(words string) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}

	return containsAny(words,
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

	return containsAny(words,
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
	if !isStrictDebugLogArtifactPath(clean) {
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

func debugLogDiscoveryRequest(mgr *workspace.Manager) (workspace.ToolRequest, bool) {
	pattern := "*.log"

	if mgr != nil {
		root := strings.TrimSpace(mgr.Root())
		for _, rel := range []string{"logs", "log", "traces", "trace"} {
			if root == "" {
				break
			}

			if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
				pattern = filepath.ToSlash(rel) + "/**/*"
				break
			}
		}
	}

	return workspace.ToolRequest{Tool: workspace.ToolNameSearchFiles, Pattern: pattern, Path: "."}, true
}

func debugLogTailRequest(selection debugLogFileSelection) workspace.ToolRequest {
	req := workspace.ToolRequest{Tool: workspace.ToolNameReadFile}
	if selection.Handle != "" {
		req.ID = selection.Handle
		return req
	}

	req.Path = selection.Path

	return req
}

func debugLogRequestTargetsSelection(req workspace.ToolRequest, selection debugLogFileSelection) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		if selection.Handle != "" && debugLogRequestHandle(req) == selection.Handle {
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

func debugLogRequestHandle(req workspace.ToolRequest) string {
	return util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Handle))
}

func debugLogSelectionMatchesPath(selection debugLogFileSelection, path string) bool {
	return normalizeRunnerPath(selection.Path) == normalizeRunnerPath(path)
}

func concreteDebugLogArtifactPathForRequest(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	for _, value := range []string{req.Path, req.Glob} {
		if isStrictDebugLogArtifactPath(value) {
			return strings.TrimSpace(value), true
		}
	}

	handle := debugLogRequestHandle(req)
	if handle == "" {
		return "", false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]

		if window, ok := parseDebugLogReadWindow(result); ok && window.Handle == handle && isStrictDebugLogArtifactPath(window.Path) {
			return window.Path, true
		}

		if hit, ok := parseDebugLogSearchHit(result); ok && hit.Handle == handle && isStrictDebugLogArtifactPath(hit.Path) {
			return hit.Path, true
		}

		for _, selection := range parseDebugLogFileSelections(result) {
			if selection.Handle == handle && isStrictDebugLogArtifactPath(selection.Path) {
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

		if selection.Handle != "" && window.Handle != selection.Handle {
			continue
		}

		if selection.Path != "" && normalizeRunnerPath(window.Path) != normalizeRunnerPath(selection.Path) {
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

func debugLogRequestTargetsCurrentSession(req workspace.ToolRequest, sessionID string) bool {
	for _, value := range []string{req.Path, req.Glob} {
		if currentSessionDebugLogPath(value, sessionID) {
			return true
		}
	}

	return false
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
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path))
	case workspace.ToolNameSearchContent:
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path)) || isStrictDebugLogArtifactPath(strings.TrimSpace(req.Glob))
	default:
		return false
	}
}

func debugLogScopedSearchRedirect(req workspace.ToolRequest, selection debugLogFileSelection) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameSearchContent || debugLogRequestTargetsSelection(req, selection) {
		return workspace.ToolRequest{}, false
	}

	if exactDebugLogFilePath(req.Path) || exactDebugLogFilePath(req.Glob) {
		return workspace.ToolRequest{}, false
	}

	if !mentionsConcreteDebugLogSelector(req.Path) && !mentionsConcreteDebugLogSelector(req.Glob) {
		return workspace.ToolRequest{}, false
	}

	redirected := req

	redirected.Path = selection.Path
	redirected.Glob = ""

	return redirected, true
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

func isStrictDebugLogArtifactPath(path string) bool {
	return mentionsConcreteDebugLogSelector(path)
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

func isDiagnosticLogArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))

	lower = strings.TrimPrefix(lower, "./")

	switch {
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


func generatedEvidenceArtifactReadRedirectResult(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolResult{}, false
	}

	path := normalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolResult{}, false
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}

	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolResult{}, false
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Generated evidence artifact %s was not read because generated discovery/context JSON is not source-body evidence.\n", path)
	b.WriteString("Use the source paths already named by the current discovery packet instead:\n")

	for _, sourcePath := range limitRunnerStrings(paths, 6) {
		fmt.Fprintf(&b, "- %s\n", sourcePath)
	}

	fmt.Fprintf(&b, "Next exact source probe example: {\"tool\":\"read_file\",\"path\":%q}\n", paths[0])
	b.WriteString("After one source-body read resolves the claim, answer from the gathered evidence instead of reading artifact JSON.")

	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Redirected generated evidence artifact read to source paths",
		Output:  strings.TrimSpace(b.String()),
	}, true
}

func generatedEvidenceArtifactReadRedirectRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolRequest{}, "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolRequest{}, "", "", false
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}

	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = paths[0]
	redirected.StartLine = 0
	redirected.EndLine = 0
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence named by generated discovery artifact")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "generated discovery/context JSON is provenance, not source-body evidence")

	return redirected, marshalToolRequestRaw(redirected), "generated discovery/context artifact redirected to source-body path", true
}

func structuredSourceReadRewriteRequest(req workspace.ToolRequest, ws *workspace.Manager, allowedTools []string, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if ws == nil || runnerBriefRequiresMutation(brief) || !redirectToolAllowed(allowedTools, workspace.ToolNameCodeNavigate) {
		return workspace.ToolRequest{}, "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", "", false
	}

	lang, cap, ok := matchRedirectRule(req, ws)
	if !ok || cap != workspace.CapFileOutline {
		return workspace.ToolRequest{}, "", "", false
	}

	path, ok := redirectReadFilePath(req, ws)
	if !ok || strings.TrimSpace(path) == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameCodeNavigate,
		Capability:      string(cap),
		Path:            normalizeRunnerPath(path),
		Language:        lang.Name,
		NamedGap:        req.NamedGap,
		WhyNotAnswering: req.WhyNotAnswering,
	}
	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "source read redirected to structured navigation", true
}

func pathlessReadSymbolAfterOutlineRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(req.Path) != "" {
		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "read_symbol" && capability != "symbol_body" && capability != "inspect_symbol" {
		return workspace.ToolRequest{}, "", "", false
	}

	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := latestCodeNavigateOutlinePath(results)
	if path == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = path

	if strings.TrimSpace(redirected.Pattern) == "" {
		redirected.Pattern = symbol
	}

	if strings.TrimSpace(redirected.Symbol) == "" {
		redirected.Symbol = symbol
	}

	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "pathless read_symbol anchored to latest file_outline path", true
}

func latestCodeNavigateOutlinePath(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}

		for _, path := range readPathsFromToolResult(result) {
			path = normalizeRunnerPath(path)
			if path != "" && !isNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			path = normalizeRunnerPath(path)
			if path != "" && !isNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}
	}

	return ""
}

func repeatedFileOutlineToReadSymbolRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "file_outline" && capability != "outline" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || concreteSourceEvidenceReadForPath(path, results) || !priorFileOutlineForPath(path, results) {
		return workspace.ToolRequest{}, "", "", false
	}

	symbol := planThenExecuteBestOutlineSymbol(path, brief.Objective, results)
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for repeated outline path")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "need a declaration body from the outlined file rather than another outline")

	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "repeated file_outline upgraded to source-body read_symbol", true
}

func priorFileOutlineForPath(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}

		for _, candidate := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			if normalizeRunnerPath(candidate) == path {
				return true
			}
		}
	}

	return false
}

func readOnlyDefinitionToReadSymbolRewriteRequest(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", "", false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsArchitecture(brief.Contract) &&
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {

		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "find_definition" && capability != "definition" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))

	if path == "" || symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = path
	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for definition target")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "definition location is anchor evidence; the answer needs the declaration body")

	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "read-only definition probe upgraded to source-body read_symbol", true
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

		if hit.Handle != "" && window.Handle != "" && hit.Handle == window.Handle && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}

		if hit.Path != "" && window.Path == hit.Path && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
	}

	return false
}

func debugLogReadWindowRequest(hit debugLogSearchHit) workspace.ToolRequest {
	start := hit.Line - 20
	if start < 1 {
		start = 1
	}

	end := hit.Line + 20
	req := workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		StartLine: start,
		EndLine:   end,
	}

	if hit.Handle != "" {
		req.ID = hit.Handle
	} else {
		req.Path = hit.Path
	}

	return req
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
	summary := strings.TrimSpace(result.Summary)
	if !strings.HasPrefix(summary, "Read ") {
		return debugLogReadWindow{}, false
	}

	body := strings.TrimPrefix(summary, "Read ")
	linesIdx := strings.Index(body, " lines ")

	if linesIdx <= 0 {
		return debugLogReadWindow{}, false
	}

	left := strings.TrimSpace(body[:linesIdx])
	right := body[linesIdx+len(" lines "):]
	rangeField := right

	if space := strings.IndexByte(rangeField, ' '); space >= 0 {
		rangeField = rangeField[:space]
	}

	rangeParts := strings.SplitN(rangeField, "-", 2)
	if len(rangeParts) != 2 {
		return debugLogReadWindow{}, false
	}

	start := parseRunnerInt(rangeParts[0])
	end := parseRunnerInt(rangeParts[1])

	if start <= 0 || end < start {
		return debugLogReadWindow{}, false
	}

	window := debugLogReadWindow{Start: start, End: end}
	leftParts := strings.SplitN(left, " ", 2)

	if len(leftParts) == 2 && strings.HasPrefix(leftParts[0], "f") {
		window.Handle = strings.TrimSpace(leftParts[0])
		window.Path = strings.TrimSpace(leftParts[1])

		return window, window.Path != ""
	}

	window.Path = left

	return window, window.Path != ""
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
