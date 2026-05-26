package agent

import (
	"fmt"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type sourceEvidenceObligation struct {
	Path        string
	Level       planThenExecuteSourceEvidenceLevel
	Source      string
	Detail      string
	PendingTool string
}

type sourceEvidenceLevel = planThenExecuteSourceEvidenceLevel

const (
	sourceEvidenceNone              sourceEvidenceLevel = planThenExecuteEvidenceNone
	sourceEvidencePathSeen          sourceEvidenceLevel = planThenExecuteEvidencePathSeen
	sourceEvidenceSourceBodyPending sourceEvidenceLevel = planThenExecuteEvidenceSourceBodyPending
	sourceEvidenceBodyWindow        sourceEvidenceLevel = planThenExecuteEvidenceBodyWindow
	sourceEvidenceAnchorReady       sourceEvidenceLevel = planThenExecuteEvidenceAnchorReady
	sourceEvidenceExactAnchor       sourceEvidenceLevel = planThenExecuteEvidenceExactAnchorContract
	sourceEvidenceNewFile           sourceEvidenceLevel = planThenExecuteEvidenceNewFileContract
)

func sourceEvidenceForPath(results []workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}
	best := planThenExecuteSourceEvidence{Path: path}
	for _, result := range results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if redirectPath := implementationTargetWriteReadyRedirectedReadPath(result); redirectPath == path {
			if tool, ok := implementationTargetWriteReadyRedirectedReadTool(result); ok {
				evidence = planThenExecuteSourceEvidence{
					Path:   path,
					Level:  sourceEvidenceSourceBodyPending,
					Source: strings.TrimSpace(result.Tool),
					Detail: "redirected to " + tool,
				}
			}
		}
		if planThenExecuteEvidenceRank(evidence.Level) > planThenExecuteEvidenceRank(best.Level) {
			best = evidence
		}
	}
	return best
}

func sourceEvidenceHasBody(evidence planThenExecuteSourceEvidence) bool {
	return planThenExecuteEvidenceRank(evidence.Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow)
}

func sourceEvidenceBodyReadForPath(path string, results []workspace.ToolResult) bool {
	return sourceEvidenceHasBody(sourceEvidenceForPath(results, path))
}

func sourceEvidenceBodyPaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range readPathsFromToolResult(result) {
			normalized := normalizeRunnerPath(path)
			if normalized == "" {
				continue
			}
			if !sourceEvidenceBodyReadForPath(normalized, results) {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, normalized)
		}
	}
	return paths
}

func sourceEvidencePendingRedirects(results []workspace.ToolResult) []sourceEvidenceObligation {
	seen := make(map[string]struct{})
	var pending []sourceEvidenceObligation
	for i := len(results) - 1; i >= 0; i-- {
		path := implementationTargetWriteReadyRedirectedReadPath(results[i])
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if sourceEvidenceBodyReadForPath(path, results) {
			continue
		}
		tool, ok := implementationTargetWriteReadyRedirectedReadTool(results[i])
		if !ok {
			continue
		}
		evidence := sourceEvidenceForPath(results, path)
		pending = append(pending, sourceEvidenceObligation{
			Path:        path,
			Level:       sourceEvidenceSourceBodyPending,
			Source:      evidence.Source,
			Detail:      evidence.Detail,
			PendingTool: tool,
		})
	}
	reverseSourceEvidenceObligations(pending)
	return pending
}

func sourceEvidencePendingRedirectsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []sourceEvidenceObligation {
	pending := sourceEvidencePendingRedirects(results)
	if len(pending) == 0 {
		return nil
	}
	if !implementationTargetSourceEvidenceReady(brief, results) {
		return pending
	}
	if !implementationTargetSourceHasScopeHints(brief) {
		return pending
	}
	var filtered []sourceEvidenceObligation
	for _, obligation := range pending {
		if sourceEvidencePendingRedirectRelevantAfterBodyEvidence(brief, obligation.Path) {
			filtered = append(filtered, obligation)
		}
	}
	return filtered
}

func sourceEvidencePendingRedirectRelevantAfterBodyEvidence(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	if implementationSourcePathOverlapsObjective(path, brief.Objective) ||
		sourceEvidencePathSharesObjectiveTerm(path, brief.Objective) {
		return true
	}
	candidates := append([]string{}, brief.RequiredEditPaths...)
	candidates = append(candidates, brief.TargetRefs...)
	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, brief.ChangedPaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(brief.Objective)...)
	for _, candidate := range candidates {
		if normalizeRunnerPath(candidate) == path {
			return true
		}
	}
	return false
}

func sourceEvidencePathSharesObjectiveTerm(path string, objective string) bool {
	terms := recoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}
	objectiveTerms := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		objectiveTerms[singularObjectiveWord(term)] = struct{}{}
	}
	for _, term := range implementationPathTerms(path) {
		if _, ok := objectiveTerms[term]; ok {
			return true
		}
	}
	return false
}

func reverseSourceEvidenceObligations(values []sourceEvidenceObligation) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func sourceEvidencePendingRedirectForPath(path string, results []workspace.ToolResult) (sourceEvidenceObligation, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return sourceEvidenceObligation{}, false
	}
	for _, obligation := range sourceEvidencePendingRedirects(results) {
		if obligation.Path == path {
			return obligation, true
		}
	}
	return sourceEvidenceObligation{}, false
}

func sourceEvidencePendingRedirectFollowupAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	tool := strings.TrimSpace(req.Tool)
	if path == "" || tool == "" {
		return false
	}
	obligation, ok := sourceEvidencePendingRedirectForPath(path, results)
	return ok && sourceEvidencePendingRedirectRequestAllowed(obligation, req)
}

func sourceEvidencePendingRedirectFollowupAllowedForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	tool := strings.TrimSpace(req.Tool)
	if path == "" || tool == "" {
		return false
	}
	for _, obligation := range sourceEvidencePendingRedirectsForBrief(brief, results) {
		if obligation.Path == path && sourceEvidencePendingRedirectRequestAllowed(obligation, req) {
			return true
		}
	}
	return false
}

func sourceEvidencePendingRedirectProgressAllowedForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	tool := strings.TrimSpace(req.Tool)
	if path == "" || tool == "" {
		return false
	}
	for _, obligation := range sourceEvidencePendingRedirectsForBrief(brief, results) {
		if obligation.Path != path {
			continue
		}
		if sourceEvidencePendingRedirectRequestAllowed(obligation, req) {
			return true
		}
		if sourceEvidencePendingRedirectOutlineProgressAllowed(obligation, req, results) {
			return true
		}
	}
	return false
}

func sourceEvidencePendingRedirectRequestAllowed(obligation sourceEvidenceObligation, req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || tool != strings.TrimSpace(obligation.PendingTool) {
		return false
	}
	switch {
	case tool == "code_navigate":
		return sourceEvidenceCodeNavigateSourceBodyRequest(req)
	case tool == "read_only_capability":
		return sourceEvidenceReadOnlyCapabilitySourceBodyRequest(req)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		return sourceEvidenceRequestHasSymbolPattern(req)
	default:
		return false
	}
}

func sourceEvidenceCodeNavigateSourceBodyRequest(req workspace.ToolRequest) bool {
	capability := sourceEvidenceCapabilityToken(firstSourceEvidenceString(req.Capability, req.Action, req.Operation, req.Mode))
	switch capability {
	case "read_symbol", "inspect_symbol", "symbol_body":
		return sourceEvidenceRequestHasSymbolPattern(req)
	default:
		return false
	}
}

func sourceEvidenceReadOnlyCapabilitySourceBodyRequest(req workspace.ToolRequest) bool {
	capability := sourceEvidenceCapabilityToken(firstSourceEvidenceString(req.Capability, req.Action, req.Operation, req.Mode))
	switch capability {
	case "read_symbol", "inspect_symbol", "symbol_body":
		return sourceEvidenceRequestHasSymbolPattern(req)
	default:
		return false
	}
}

func sourceEvidencePendingRedirectOutlineProgressAllowed(obligation sourceEvidenceObligation, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if normalizeRunnerPath(req.Path) != strings.TrimSpace(obligation.Path) {
		return false
	}
	if sourceEvidenceOutlineReadForPath(obligation.Path, results) {
		return false
	}
	if strings.TrimSpace(req.NamedGap) == "" && strings.TrimSpace(req.WhyNotAnswering) == "" {
		return false
	}
	tool := strings.TrimSpace(req.Tool)
	pendingTool := strings.TrimSpace(obligation.PendingTool)
	if tool == "" {
		return false
	}
	if outlineTool := sourceEvidencePendingRedirectOutlineTool(pendingTool); outlineTool != "" && tool == outlineTool {
		return true
	}
	if tool != pendingTool {
		return false
	}
	switch tool {
	case "code_navigate", "read_only_capability":
		return sourceEvidenceCapabilityIsOutline(firstSourceEvidenceString(req.Capability, req.Action, req.Operation, req.Mode))
	default:
		return false
	}
}

func sourceEvidenceOutlineReadForPath(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := strings.TrimSpace(result.Tool)
		if tool != "code_navigate" && tool != "read_only_capability" && !strings.HasSuffix(tool, "_file_outline") {
			continue
		}
		if !sourceEvidenceToolResultMentionsPath(result, path) {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") ||
			strings.HasSuffix(tool, "_file_outline") {
			return true
		}
	}
	return false
}

func sourceEvidenceToolResultMentionsPath(result workspace.ToolResult, path string) bool {
	for _, readPath := range readPathsFromToolResult(result) {
		if normalizeRunnerPath(readPath) == path {
			return true
		}
	}
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
	return strings.Contains(text, strings.ToLower(path))
}

func sourceEvidenceCapabilityIsOutline(value string) bool {
	switch sourceEvidenceCapabilityToken(value) {
	case "file_outline", "outline":
		return true
	default:
		return false
	}
}

func sourceEvidencePendingRedirectOutlineTool(tool string) string {
	tool = strings.TrimSpace(tool)
	switch {
	case strings.HasSuffix(tool, "_read_symbol"):
		return strings.TrimSuffix(tool, "_read_symbol") + "_file_outline"
	case strings.HasSuffix(tool, "_extract_body"):
		return strings.TrimSuffix(tool, "_extract_body") + "_file_outline"
	default:
		return ""
	}
}

func sourceEvidenceRequestHasSymbolPattern(req workspace.ToolRequest) bool {
	return strings.TrimSpace(firstSourceEvidenceString(req.Pattern, req.Symbol, req.Query)) != ""
}

func sourceEvidenceCapabilityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func firstSourceEvidenceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sourceEvidencePendingRedirectTools(baseTools []string, pending []sourceEvidenceObligation) []string {
	if len(pending) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(baseTools))
	for _, tool := range baseTools {
		available[strings.TrimSpace(tool)] = struct{}{}
	}
	seen := make(map[string]struct{})
	var tools []string
	for _, obligation := range pending {
		for _, tool := range []string{
			strings.TrimSpace(obligation.PendingTool),
			sourceEvidencePendingRedirectOutlineTool(obligation.PendingTool),
		} {
			if tool == "" {
				continue
			}
			if _, ok := available[tool]; !ok {
				continue
			}
			if _, ok := seen[tool]; ok {
				continue
			}
			seen[tool] = struct{}{}
			tools = append(tools, tool)
		}
	}
	return tools
}

func sourceEvidencePendingRedirectSummary(pending []sourceEvidenceObligation, max int) string {
	if max <= 0 {
		max = len(pending)
	}
	var parts []string
	for i, obligation := range pending {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
			break
		}
		path := strings.TrimSpace(obligation.Path)
		tool := strings.TrimSpace(obligation.PendingTool)
		if path == "" || tool == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s via %s", path, tool))
	}
	return strings.Join(parts, ", ")
}

func sourceEvidencePendingRedirectActionSummary(pending []sourceEvidenceObligation, max int) string {
	if max <= 0 {
		max = len(pending)
	}
	var parts []string
	for i, obligation := range pending {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
			break
		}
		if label := sourceEvidencePendingRedirectActionLabel(obligation); label != "" {
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, ", ")
}

func sourceEvidencePendingRedirectActionLabel(obligation sourceEvidenceObligation) string {
	path := strings.TrimSpace(obligation.Path)
	tool := strings.TrimSpace(obligation.PendingTool)
	if path == "" || tool == "" {
		return ""
	}
	switch {
	case tool == "code_navigate":
		return fmt.Sprintf("%s via code_navigate capability=read_symbol with pattern/symbol, or one file_outline first when the symbol is unknown", path)
	case tool == "read_only_capability":
		return fmt.Sprintf("%s via read_only_capability inspect-symbol with pattern/symbol, or one outline first when the symbol is unknown", path)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		if outlineTool := sourceEvidencePendingRedirectOutlineTool(tool); outlineTool != "" {
			return fmt.Sprintf("%s via %s with pattern/symbol, or one %s same-path outline first when the symbol is unknown", path, tool, outlineTool)
		}
		return fmt.Sprintf("%s via %s with pattern/symbol", path, tool)
	default:
		return fmt.Sprintf("%s via %s source-body capability", path, tool)
	}
}
