package app

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const (
	implementationFollowupContextMaxBytes      = 6000
	implementationFollowupPriorAssistantMarker = "conversation_context:previous_assistant_recommendation"
)

func promptExecutionPlanWithConversationContext(prompt string, history []llama.ChatMessage, plan promptExecutionPlan, workspaceRoot ...string) promptExecutionPlan {
	plan.runtimeBrief = implementationFollowupRuntimeBrief(prompt, history, plan.route, plan.runtimeBrief, firstImplementationFollowupWorkspaceRoot(workspaceRoot))

	return plan
}

func implementationFollowupRuntimeBrief(prompt string, history []llama.ChatMessage, route turnRoutePlan, brief workerruntime.WorkerRuntimeBrief, workspaceRoot ...string) workerruntime.WorkerRuntimeBrief {
	if !implementationFollowupShouldCarryPriorAnswer(prompt, route, brief) {
		return workerruntime.NormalizeRuntimeBrief(brief)
	}

	priorAnswer, ok := lastAssistantAnswer(history)
	if !ok {
		return workerruntime.NormalizeRuntimeBrief(brief)
	}

	return applyImplementationFollowupPriorAnswer(brief, priorAnswer, implementationFollowupRecommendedEditPaths(priorAnswer, history), firstImplementationFollowupWorkspaceRoot(workspaceRoot))
}

func firstImplementationFollowupWorkspaceRoot(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return strings.TrimSpace(values[0])
}

func applyImplementationFollowupPriorAnswer(brief workerruntime.WorkerRuntimeBrief, priorAnswer string, candidatePaths []string, workspaceRoot string) workerruntime.WorkerRuntimeBrief {
	body := strings.TrimSpace(priorAnswer)
	if body == "" {
		return workerruntime.NormalizeRuntimeBrief(brief)
	}

	explicitScope := len(brief.RequiredEditPaths) > 0 ||
		len(brief.AllowedEditPaths) > 0 ||
		len(brief.RequiredEditSnippets) > 0 ||
		len(brief.AuthorizedNewFiles) > 0
	canPromoteCandidates := workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractImplementationResult) &&
		!workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) &&
		!workerruntime.TaskFamilyIsPlanThenExecuteReview(brief.TaskFamily) &&
		(workerruntime.PermissionProfileMatches(brief.PermissionProfile, workerruntime.PermissionGuardedWorkspaceWrite) ||
			workerruntime.PermissionProfileMatches(brief.PermissionProfile, workerruntime.PermissionAnalysisReadOnlyThenWrite))
	brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket,
		implementationFollowupPriorAssistantMarker,
		implementationFollowupPriorAnswerPacket(body, explicitScope),
	)

	candidates := implementationFollowupClassifyCandidatePaths(candidatePaths, priorAnswer, workspaceRoot)
	if canPromoteCandidates && !explicitScope && len(candidates.ExistingFiles) > 0 {
		brief.AllowedEditPaths = appendUniqueBriefStrings(brief.AllowedEditPaths, candidates.ExistingFiles...)
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, candidates.ExistingFiles...)
		for _, path := range candidates.ExistingFiles {
			brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, "Editable candidate file from previous assistant recommendation: "+path)
		}
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
			"treat conversation_context verified existing candidate files as prior-turn edit scope, not required coverage obligations",
		)
	}
	if canPromoteCandidates && !explicitScope && len(candidates.AuthorizedNewFiles) > 0 {
		brief.AuthorizedNewFiles = appendUniqueBriefStrings(brief.AuthorizedNewFiles, candidates.AuthorizedNewFiles...)
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, candidates.AuthorizedNewFiles...)
		for _, path := range candidates.AuthorizedNewFiles {
			brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, "Authorized new file from previous assistant recommendation: "+path)
		}
	}
	for _, path := range candidates.UnresolvedPaths {
		brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, "Unresolved prior recommendation path; not granted as edit scope until verified or explicitly authorized as a new file: "+path)
	}

	brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
		"when the objective refers to a prior recommendation, use the conversation_context target packet to recover the requested edit before rediscovery",
	)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

type implementationFollowupCandidatePaths struct {
	ExistingFiles      []string
	AuthorizedNewFiles []string
	UnresolvedPaths    []string
}

func implementationFollowupClassifyCandidatePaths(candidatePaths []string, priorAnswer string, workspaceRoot string) implementationFollowupCandidatePaths {
	var classified implementationFollowupCandidatePaths
	candidatePaths = appendUniqueBriefStrings(nil, candidatePaths...)
	authorizedNewFiles := plancontract.AuthorizedNewFilePathsFromPrompt(priorAnswer)
	root := strings.TrimSpace(workspaceRoot)

	for _, candidate := range candidatePaths {
		normalized, ok := implementationFollowupNormalizeCandidatePath(candidate)
		if !ok {
			continue
		}

		if root == "" || implementationFollowupPathIsExistingFile(root, normalized) {
			classified.ExistingFiles = appendUniqueBriefStrings(classified.ExistingFiles, normalized)
			continue
		}

		if slices.Contains(authorizedNewFiles, normalized) {
			classified.AuthorizedNewFiles = appendUniqueBriefStrings(classified.AuthorizedNewFiles, normalized)
			continue
		}

		classified.UnresolvedPaths = appendUniqueBriefStrings(classified.UnresolvedPaths, normalized)
	}

	return classified
}

func implementationFollowupNormalizeCandidatePath(candidate string) (string, bool) {
	candidate = strings.TrimSpace(filepath.ToSlash(candidate))
	if candidate == "" || strings.HasPrefix(candidate, "/") || strings.Contains(candidate, "\x00") {
		return "", false
	}

	normalized := path.Clean(candidate)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", false
	}

	return normalized, true
}

func implementationFollowupPathIsExistingFile(root string, candidate string) bool {
	root = strings.TrimSpace(root)
	candidate, ok := implementationFollowupNormalizeCandidatePath(candidate)
	if root == "" || !ok {
		return false
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absRoot = filepath.Clean(absRoot)
	absCandidate := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(candidate)))
	if rel, err := filepath.Rel(absRoot, absCandidate); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	info, err := os.Stat(absCandidate)
	return err == nil && !info.IsDir()
}

func implementationFollowupPriorAnswerPacket(body string, explicitScope bool) string {
	body = textutil.Truncate(strings.TrimSpace(body), implementationFollowupContextMaxBytes)
	if !explicitScope {
		return "Previous assistant recommendation referenced by the current implementation request:\n" + body
	}

	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, "read_only_evidence previous assistant recommendation: "+line)
		}
	}

	return "Previous assistant recommendation referenced by the current implementation request; current-turn edit scope remains authoritative:\n" + strings.Join(lines, "\n")
}

func implementationFollowupShouldCarryPriorAnswer(prompt string, route turnRoutePlan, brief workerruntime.WorkerRuntimeBrief) bool {
	if !workerruntime.TaskFamilyMutationCapable(route.Family) && !workerruntime.TaskFamilyMutationCapable(brief.TaskFamily) {
		return false
	}

	words := strings.ToLower(strings.TrimSpace(prompt))
	if words == "" {
		return false
	}

	return textContainsAny(words, []string{
		"you recommended",
		"you suggested",
		"as above",
		"previous answer",
		"prior answer",
		"last answer",
		"you said",
		"these values",
		"those values",
		"these changes",
		"those changes",
		"that recommendation",
		"that fix",
		"move these",
		"apply these",
		"locations you",
	})
}

func implementationFollowupRecommendedEditPaths(priorAnswer string, history []llama.ChatMessage) []string {
	knownPaths := implementationFollowupKnownWorkspacePaths(history)
	var candidates []string
	recommendationSection := implementationFollowupRecommendationSection{}

	for _, line := range strings.Split(priorAnswer, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			recommendationSection.consumeBlankLine()
			continue
		}

		linePaths := implementationFollowupWorkspacePathsFromText(trimmed, knownPaths)
		describesEditScope := implementationFollowupLineDescribesEditScope(trimmed)
		inRecommendationSection := recommendationSection.consumeLine(trimmed, len(linePaths) > 0)
		if describesEditScope || inRecommendationSection {
			candidates = appendUniqueBriefStrings(candidates, linePaths...)
		}
	}

	return candidates
}

type implementationFollowupRecommendationSection struct {
	active       bool
	sawPath      bool
	preludeLines int
}

func (section *implementationFollowupRecommendationSection) consumeBlankLine() {
	if section.sawPath {
		*section = implementationFollowupRecommendationSection{}
	}
}

func (section *implementationFollowupRecommendationSection) consumeLine(line string, hasPaths bool) bool {
	startedSection := implementationFollowupLineStartsRecommendationSection(line)
	if startedSection {
		section.active = true
		section.sawPath = false
		section.preludeLines = 1
	}

	if !section.active {
		return false
	}

	if hasPaths {
		section.sawPath = true
		section.preludeLines = 0
		return true
	}

	if startedSection {
		return true
	}

	if section.sawPath {
		*section = implementationFollowupRecommendationSection{}
		return false
	}

	if section.preludeLines > 0 {
		section.preludeLines--
		return true
	}

	*section = implementationFollowupRecommendationSection{}

	return false
}

func implementationFollowupLineStartsRecommendationSection(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" {
		return false
	}

	return textContainsAny(normalized, []string{
		"where it should go",
		"where they should go",
		"should go:",
		"destination:",
		"candidate file:",
		"candidate files:",
		"editable file:",
		"editable files:",
		"editable candidate file:",
		"editable candidate files:",
	})
}

func implementationFollowupLineDescribesEditScope(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" {
		return false
	}

	if strings.Contains(normalized, "not ") && (strings.Contains(normalized, "edit") || strings.Contains(normalized, "modify")) {
		return false
	}

	return textContainsAny(normalized, []string{
		"the fix",
		"fix:",
		"where it should go",
		"where they should go",
		"should go:",
		"should be defined",
		"should be named",
		"should become",
		"defined as typed constant",
		"defined as a typed constant",
		"become named constant",
		"become typed constant",
		"constants belong",
		"missing constants belong",
		"belong in",
		"belongs in",
		"named constants in",
		"typed constants in",
		"add ",
		"replace ",
		"move ",
		"edit ",
		"modify ",
		"update ",
		"reference these",
		"references these",
		"would reference",
		"should reference",
		"destination:",
		"candidate file:",
		"candidate files:",
		"editable file:",
		"editable files:",
		"editable candidate file:",
		"editable candidate files:",
	})
}

func implementationFollowupWorkspacePathsFromText(text string, knownPaths []string) []string {
	paths := plancontract.PromptWorkspacePaths(text)

	for i, path := range paths {
		if strings.Contains(path, "/") {
			continue
		}

		if resolved, ok := implementationFollowupResolveBasename(path, knownPaths); ok {
			paths[i] = resolved
		}
	}

	return appendUniqueBriefStrings(nil, paths...)
}

func implementationFollowupKnownWorkspacePaths(history []llama.ChatMessage) []string {
	var paths []string
	for _, msg := range history {
		paths = appendUniqueBriefStrings(paths, plancontract.PromptWorkspacePaths(msg.Content)...)
	}

	return paths
}

func implementationFollowupResolveBasename(path string, knownPaths []string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "/") {
		return "", false
	}

	var match string
	for _, known := range knownPaths {
		if strings.TrimSpace(known) == "" || !strings.HasSuffix(known, "/"+path) {
			continue
		}

		if match != "" && match != known {
			return "", false
		}

		match = known
	}

	if match != "" {
		return match, true
	}

	dir := implementationFollowupSingleKnownDir(knownPaths)
	if dir == "" || !strings.Contains(path, ".") {
		return "", false
	}

	return dir + "/" + path, true
}

func implementationFollowupSingleKnownDir(knownPaths []string) string {
	var dir string
	for _, known := range knownPaths {
		known = strings.TrimSpace(known)
		if known == "" || !strings.Contains(known, "/") {
			continue
		}

		candidate := path.Dir(known)
		if candidate == "." || candidate == "/" {
			continue
		}

		if dir != "" && dir != candidate {
			return ""
		}

		dir = candidate
	}

	return dir
}
