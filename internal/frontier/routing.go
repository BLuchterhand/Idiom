package frontier

import "strings"

type RouteProfile struct {
	Normalized                         string
	WantsLocate                        bool
	WantsDirectImplement               bool
	WantsImplementation                bool
	WantsExploration                   bool
	WantsReview                        bool
	WantsArchitecture                  bool
	WantsPlanOnly                      bool
	WantsVerificationOnly              bool
	WantsDebugLogs                     bool
	HasReadOnlyBoundary                bool
	HasConcreteTarget                  bool
	Thoroughness                       string
	RequestsAnalysisBeforeImplementing bool
}

type ContextualRouteInput struct {
	Prompt                     string
	Recent                     string
	DefaultThoroughness        string
	HasRecentReplanToImplement bool
}

type ContextualRouteDecision struct {
	Plan         IntentPlan
	Thoroughness string
}

func BuildRouteProfile(prompt string) RouteProfile {
	normalized := strings.ToLower(prompt)
	thoroughness, wantsExploration := AutoExploreThoroughness(prompt)
	wantsDebugLogs := LooksLikeLogOrFailureInspectionPrompt(normalized)
	wantsVerificationOnly := LooksLikeVerificationOnlyPrompt(normalized)
	wantsPlanOnly := LooksLikePlanOnlyPrompt(normalized)
	hasReadOnlyBoundary := LooksLikeReadOnlyBoundaryPrompt(normalized)
	conversationMemoryNote := LooksLikeConversationMemoryNotePrompt(normalized)
	wantsReview := LooksLikeReviewPrompt(normalized)
	if wantsDebugLogs {
		wantsExploration = true
		if thoroughness == "" {
			thoroughness = "medium"
		}
	}
	wantsArchitecture := LooksLikeArchitecturePrompt(normalized) || LooksLikeArchitecturePlanRequest(normalized)
	wantsDirectImplement := LooksLikeDirectImplementationPrompt(prompt, normalized)
	wantsImplementation := PromptRequestsImplementation(prompt)
	if conversationMemoryNote {
		wantsDirectImplement = false
		wantsImplementation = false
		wantsExploration = false
		wantsReview = false
		wantsArchitecture = false
		wantsVerificationOnly = false
	}
	return RouteProfile{
		Normalized:                         normalized,
		WantsLocate:                        LooksLikeLocatePrompt(prompt, normalized) || ShouldRouteUILiteralEditThroughLocate(prompt, normalized),
		WantsDirectImplement:               wantsDirectImplement,
		WantsImplementation:                wantsImplementation,
		WantsExploration:                   wantsExploration,
		WantsReview:                        wantsReview,
		WantsArchitecture:                  wantsArchitecture,
		WantsPlanOnly:                      wantsPlanOnly,
		WantsVerificationOnly:              wantsVerificationOnly,
		WantsDebugLogs:                     wantsDebugLogs,
		HasReadOnlyBoundary:                hasReadOnlyBoundary,
		HasConcreteTarget:                  len(PromptMentionedWorkspacePaths(prompt)) > 0,
		Thoroughness:                       thoroughness,
		RequestsAnalysisBeforeImplementing: PromptRequestsAnalysisBeforeImplementation(normalized),
	}
}

func LooksLikeConversationMemoryNotePrompt(normalized string) bool {
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return false
	}
	for _, prefix := range []string{
		"remember that ",
		"also remember that ",
		"track that ",
		"keep the current focus ",
		"note that ",
		"record that ",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return !ContainsAny(trimmed, " now ", " now.", " now,", " now:", " right now", " immediately")
		}
	}
	return false
}

func InitialRoute(profile RouteProfile) (Lane, RouteReason) {
	switch {
	case profile.WantsArchitecture && (profile.WantsPlanOnly || profile.WantsExploration):
		return LaneArchitecture, RouteReasonArchitectureRequest
	case profile.WantsLocate:
		return LaneLocate, RouteReasonLocateRequest
	case profile.WantsDirectImplement && !profile.RequestsAnalysisBeforeImplementing && (profile.HasConcreteTarget || !profile.WantsExploration):
		return LaneImplement, RouteReasonDirectImplementation
	case profile.WantsArchitecture:
		return LaneArchitecture, RouteReasonArchitectureRequest
	case profile.WantsDebugLogs:
		return LaneExplore, RouteReasonExploreRequest
	case profile.WantsExploration && profile.WantsReview:
		return LaneReview, RouteReasonReviewRequest
	case profile.WantsExploration:
		return LaneExplore, RouteReasonExploreRequest
	default:
		return LaneMain, RouteReasonDirectConversation
	}
}

func FollowupRoute(profile RouteProfile, lane Lane) Lane {
	if !ProfileWantsEditIntent(profile) {
		return ""
	}
	switch lane {
	case LaneLocate, LaneExplore, LaneReview, LaneArchitecture:
		return LaneImplement
	default:
		return ""
	}
}

func ProfileWantsEditIntent(profile RouteProfile) bool {
	return profile.WantsImplementation || profile.WantsDirectImplement
}

func ContextualRoute(input ContextualRouteInput) (ContextualRouteDecision, bool) {
	normalized := strings.ToLower(strings.TrimSpace(input.Prompt))
	if normalized == "" {
		return ContextualRouteDecision{}, false
	}
	recent := strings.ToLower(strings.TrimSpace(input.Recent))
	if (LooksLikeArchitectureValidationFollowup(normalized) || LooksLikeArchitecturePlanningFollowup(normalized)) &&
		RecentContextSupportsArchitectureValidation(recent) {
		thoroughness := strings.TrimSpace(input.DefaultThoroughness)
		if thoroughness == "" {
			thoroughness = "medium"
		}
		return ContextualRouteDecision{
			Plan: IntentPlan{
				Lane:   LaneArchitecture,
				Reason: RouteReasonArchitectureRequest,
			},
			Thoroughness: thoroughness,
		}, true
	}
	if LooksLikeEllipticalImplementationFollowup(normalized) &&
		RecentContextSupportsImplementation(recent, input.HasRecentReplanToImplement) {
		return ContextualRouteDecision{
			Plan: IntentPlan{
				Lane:   LaneImplement,
				Reason: RouteReasonDirectImplementation,
			},
		}, true
	}
	return ContextualRouteDecision{}, false
}

func LooksLikeArchitectureValidationFollowup(normalized string) bool {
	return ContainsAny(normalized, "validate", "confirm", "assess", "check") &&
		ContainsAny(normalized, "option", "recommendation", "analysis", "architecture", "architectural", "refactor", "split", "boundary")
}

func LooksLikeArchitecturePlanningFollowup(normalized string) bool {
	return LooksLikePlanOnlyPrompt(normalized) &&
		ContainsAny(normalized, "update", "refactor", "optimize", "architecture", "architectural", "split", "boundary", "option", "this one", "this file")
}

func RecentContextSupportsArchitectureValidation(recent string) bool {
	if strings.TrimSpace(recent) == "" {
		return false
	}
	return len(PromptMentionedWorkspacePaths(recent)) > 0 &&
		ContainsAny(recent, "architecture", "architectural", "hotspot", "structural", "boundary", "refactor", "optimization opportunity")
}

func RecentContextSupportsImplementation(recent string, hasRecentReplanToImplement bool) bool {
	if hasRecentReplanToImplement {
		return true
	}
	if strings.TrimSpace(recent) == "" {
		return false
	}
	hasConcreteTarget := len(PromptMentionedWorkspacePaths(recent)) > 0
	hasImplementationCue := ContainsAny(
		recent,
		"recommended implementation order",
		"implementation order",
		"recommended next step",
		"suggested refactor",
		"apply changes",
		"apply the fix",
		"make the change",
		"implement it",
		"then implement",
		"split one responsibility",
		"extract state management",
		"create prompt generation",
		"extract output formatting",
		"consolidate error",
	)
	hasStructuralCue := ContainsAny(recent, "architecture", "architectural", "refactor", "split", "boundary", "hotspot", "optimization opportunity")
	return hasConcreteTarget && hasImplementationCue && hasStructuralCue
}

func LooksLikeLogOrFailureInspectionPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	if ContainsAny(normalized, "latest log", "recent log", "most recent log", "check the logs", "read the logs", "logs?") {
		return true
	}
	if ContainsAny(normalized, "why did this fail", "why this failed", "it failed", "this failed", "failed again") {
		return true
	}
	return ContainsAny(normalized, "failure output", "stack trace", "traceback") &&
		ContainsAny(normalized, "check", "read", "inspect", "debug", "diagnose", "why")
}

func PromptRequestsWholeFileRefactor(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return ContainsAny(normalized, "refactor", "split", "reorganize", "move code") &&
		ContainsAny(normalized, "whole file", "entire file", "in one shot", "split existing code", "monolithic", "cohesive new files", "multiple files", "multi-file")
}

func LooksLikeVerificationOnlyPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	if LooksLikeLogOrFailureInspectionPrompt(normalized) {
		return false
	}
	if PromptRequestsImplementation(normalized) {
		return false
	}
	if ContainsAny(normalized, "add ", "create ", "write ", "update ", "implement ", "fix ", "patch ", "edit ") {
		return false
	}
	hasVerificationVerb := ContainsAny(normalized, "run ", "verify", "check", "test ", "report")
	hasVerificationTarget := ContainsAny(normalized, "test", "tests", "suite", "build", "lint", "check", "verification")
	return hasVerificationVerb && hasVerificationTarget
}

func PromptRequestsAnalysisBeforeImplementation(normalized string) bool {
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return false
	}
	if ContainsAny(normalized, "identify it first", "show it first", "before changing", "before you change", "don't change it yet", "do not change it yet") {
		return true
	}
	for _, prefix := range []string{"explore ", "investigate ", "analyze ", "identify ", "review ", "audit ", "look through ", "inspect "} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func PromptRequestsImplementation(prompt string) bool {
	normalized := strings.ToLower(prompt)
	trimmed := strings.TrimSpace(normalized)
	if LooksLikePlanOnlyPrompt(trimmed) {
		return false
	}
	if strings.HasPrefix(trimmed, "fix ") || strings.HasPrefix(trimmed, "patch ") || strings.HasPrefix(trimmed, "implement ") || strings.HasPrefix(trimmed, "refactor ") {
		return true
	}
	if PrimaryLocateLiteral(prompt) != "" && ContainsAny(normalized, "remove ", "delete ", "take it out") {
		return true
	}
	return ContainsAny(normalized,
		"refactor ",
		"split existing code",
		"produce the refactor",
		"in one shot",
		"fix it now",
		"fix this now",
		"fix the issue",
		"fix the failure",
		"fix the bug",
		"please fix",
		"go fix",
		"patch it",
		"patch this",
		"apply the fix",
		"apply that fix",
		"apply this fix",
		"apply changes now",
		"write the fix",
		"write the patch",
		"edit the file",
		"edit those files",
		"update the code",
		"change the code",
		"implement the fix",
		"make the fix",
		"make the change",
		"create the file",
		"delete the file",
		"remove it",
		"remove this",
		"remove the code",
		"delete it",
		"refactor it",
		"refactor this",
		"then fix",
		"then fix it",
		"then update",
		"then implement",
		"then implement it",
		"and fix",
		"and fix it",
		"and update",
		"and implement",
		"and implement it",
	)
}

func LooksLikeDirectImplementationPrompt(prompt string, normalized string) bool {
	if LooksLikePlanOnlyPrompt(normalized) {
		return false
	}
	if PromptRequestsImplementation(prompt) {
		return true
	}
	if RequestsShellOnlyMutation(normalized) && (len(PromptMentionedWorkspacePaths(prompt)) > 0 || ContainsAny(normalized, "file ", "directory ", "folder ")) {
		return true
	}
	if ContainsAny(normalized, "use edit_file", "targeted edit", "code edit", "change the footer help text", "replace ", "rename ") {
		return len(PromptMentionedWorkspacePaths(prompt)) > 0
	}
	hasEditVerb := ContainsAny(
		normalized,
		"create",
		"edit",
		"change",
		"replace",
		"update",
		"modify",
		"rename",
		"remove",
		"delete",
		"add",
		"refactor",
		"split",
		"reorganize",
		"write",
		"append",
		"mkdir",
	)
	hasConcreteTarget := len(PromptMentionedWorkspacePaths(prompt)) > 0 ||
		ContainsAny(normalized, "line ", "function ", "method ", "text from", "text to") ||
		HasUILocationHint(normalized)
	return hasEditVerb && hasConcreteTarget && !LooksLikeInvestigationPrompt(normalized)
}

func LooksLikePlanOnlyPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	hasPlanRequest := ContainsAny(
		normalized,
		"prepare a plan",
		"prepare an plan",
		"prepare a refactor plan",
		"prepare a refactoring plan",
		"plan a refactor",
		"plan the refactor",
		"plan this refactor",
		"plan that refactor",
		"plan a refactoring",
		"plan the refactoring",
		"plan this refactoring",
		"plan that refactoring",
		"make a plan",
		"create a plan",
		"propose a plan",
		"propose a work plan",
		"write a plan",
		"draft a plan",
		"work plan",
		"cleanup plan",
		"refactor cleanup plan",
		"refactoring cleanup plan",
		"work out an approach",
		"work out approach",
		"outline an approach",
		"approach to refactoring",
		"approach to refactor",
		"implementation plan",
		"refactor plan",
		"refactoring plan",
		"plan to update",
		"plan to refactor",
		"plan for updating",
		"plan for refactoring",
	)
	if !hasPlanRequest {
		return false
	}
	return !ContainsAny(
		normalized,
		"then implement",
		"and implement",
		"then update",
		"and update",
		"then apply",
		"and apply",
		"apply the plan",
		"execute the plan",
		"do it now",
		"make the changes",
	)
}

func LooksLikeReadOnlyBoundaryPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	return ContainsAny(
		normalized,
		"don't edit",
		"do not edit",
		"don't change",
		"do not change",
		"don't modify",
		"do not modify",
		"read only",
		"read-only",
		"plan only",
		"no edits",
		"without editing",
		"without changing",
		"before editing",
		"before changing",
	)
}

func LooksLikeOptimizationWorkflowPrompt(prompt string, profile RouteProfile) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" || !profile.WantsArchitecture {
		return false
	}
	return ContainsAny(
		normalized,
		"optimization opportunity",
		"optimization opportunities",
		"optimize our architecture",
		"optimize the architecture",
		"refactor opportunity",
		"refactor opportunities",
		"refactoring opportunity",
		"refactoring opportunities",
		"plan a refactor",
		"plan the refactor",
		"refactor plan",
		"refactoring plan",
		"maintainability",
		"package boundary",
		"package boundaries",
	)
}

func LooksLikeWorkflowApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.ReplaceAll(normalized, "’", "'")
	trimmed := strings.Trim(normalized, " \t\r\n.!?")
	if trimmed == "" {
		return false
	}
	if ContainsAny(trimmed, "don't", "do not", "not yet", "hold off", "wait", "without performing", "without applying", "without implementing") {
		return false
	}
	if LooksLikeDurablePlanApprovalPrompt(trimmed) || LooksLikeTerseDurablePlanApprovalPrompt(trimmed) {
		return true
	}
	switch trimmed {
	case "commence",
		"please commence",
		"let's commence",
		"lets commence",
		"build it",
		"please build it",
		"build this",
		"build that",
		"execute it",
		"please execute it",
		"apply all of it",
		"implement everything",
		"perform it":
		return true
	default:
		return ContainsAny(trimmed, "commence", "start building", "begin building", "perform the approved plan", "execute the approved plan")
	}
}

func LooksLikeResumeStructuralRefactorPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return ContainsAny(normalized, "continue", "resume", "next stage", "next refactor") &&
		ContainsAny(normalized, "refactor", "structural", "architecture", "stage")
}

func LooksLikeResumeTaskMemoryPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return ContainsAny(normalized, "continue", "resume", "follow up", "next step", "pick up") &&
		ContainsAny(normalized, "task", "investigation", "search", "repair", "evidence", "debug", "issue", "failure")
}

func LooksLikeArchitecturePlanRequest(normalized string) bool {
	return LooksLikePlanOnlyPrompt(normalized) &&
		ContainsAny(
			normalized,
			"architecture",
			"architectural",
			"structure",
			"structural",
			"boundary",
			"boundaries",
			"refactor",
			"refactoring",
			"split",
			"decompose",
			"modular",
			"responsibility",
			"responsibilities",
		)
}

func AutoExploreThoroughness(prompt string) (string, bool) {
	normalized := strings.ToLower(prompt)
	if !LooksLikeInvestigationPrompt(normalized) {
		return "", false
	}
	switch {
	case LooksLikeNoEarlyStopPrompt(normalized):
		return "thorough", true
	case ContainsAny(normalized, "thorough", "deep", "exhaustive"):
		return "thorough", true
	case ContainsAny(normalized, "quick", "brief", "fast"):
		return "quick", true
	default:
		return "medium", true
	}
}

func LooksLikeNoEarlyStopPrompt(normalized string) bool {
	return ContainsAny(
		normalized,
		"longer search",
		"give up early",
		"not super confident",
		"not confident",
		"do not stop",
		"don't stop",
		"continue until",
		"keep searching",
		"proper frontier",
		"frontier-grade",
		"whole codebase",
		"entire codebase",
		"literally the whole codebase",
		"comprehensive",
	)
}

func LooksLikeInvestigationPrompt(normalized string) bool {
	if ContainsAny(normalized, "just explore", "what's wrong", "what is wrong") {
		return true
	}

	hasInvestigationVerb := ContainsAny(
		normalized,
		"explore",
		"discover",
		"discovery",
		"investigate",
		"investigation",
		"inspect",
		"audit",
		"review",
		"look for",
		"look through",
		"search",
		"scan",
		"identify",
		"find",
		"analyze",
		"diagnose",
	)
	hasTarget := ContainsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"improvement",
		"improvements",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"routing",
		"flow",
		"architecture",
		"architectural",
		"structure",
	)
	hasScope := ContainsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"code",
	)
	hasArchitectureQuestion := ContainsAny(
		normalized,
		"can you identify",
		"how does",
		"how do",
		"how are",
		"how is",
		"why does",
		"why is",
	)
	hasArchitectureTarget := ContainsAny(
		normalized,
		"support",
		"supported",
		"extensible",
		"extension",
		"architecture",
		"pipeline",
		"workflow",
		"language",
		"languages",
	)
	hasOpenEndedReviewQuestion := ContainsAny(
		normalized,
		"what can we",
		"what should we",
		"anything to",
		"where should we",
	)

	return (hasInvestigationVerb && hasTarget) ||
		(hasInvestigationVerb && hasScope) ||
		(hasOpenEndedReviewQuestion && hasTarget && hasScope) ||
		(hasArchitectureQuestion && hasScope && hasArchitectureTarget)
}

func LooksLikeArchitecturePrompt(normalized string) bool {
	hasScope := ContainsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"system",
		"code",
	)
	hasStructureTarget := ContainsAny(
		normalized,
		"structure",
		"architecture",
		"architectural",
		"organize",
		"organization",
		"modular",
		"modularity",
		"boundary",
		"boundaries",
		"split",
		"decompose",
		"extensible",
		"extension",
		"support",
		"supported",
		"capability",
		"capabilities",
		"language",
		"languages",
		"pipeline",
		"workflow",
		"maintainability",
		"long-term maintainability",
		"long term maintainability",
		"responsibility",
		"responsibilities",
	)
	hasArchitectureQuestion := ContainsAny(
		normalized,
		"how does",
		"how do",
		"how are",
		"how is",
		"how should",
		"why does",
		"why is",
		"what can we",
		"what should we",
		"where should we",
		"which language",
		"which languages",
	)
	hasStructuralImprovementRequest := ContainsAny(
		normalized,
		"architectural improvement",
		"architectural improvements",
		"architecture improvement",
		"architecture improvements",
		"optimize our architecture",
		"optimize the architecture",
		"optimize architecture",
		"optimize our architectural",
		"improve our architecture",
		"improve the architecture",
		"improve architecture",
		"structural improvement",
		"structural improvements",
		"optimization opportunity",
		"optimization opportunities",
		"discovery optimization",
		"discover optimization",
		"maintainability",
		"long-term maintainability",
		"long term maintainability",
		"refactor opportunity",
		"refactor opportunities",
		"refactoring opportunity",
		"refactoring opportunities",
		"long-term refactor",
		"long term refactor",
		"package boundary",
		"package boundaries",
	)
	hasExplicitArchitectureRequest := ContainsAny(
		normalized,
		"analyze the architecture",
		"analyse the architecture",
		"review the architecture",
		"assess the architecture",
		"map the architecture",
		"architecture and then",
		"architecture plan",
		"architecture approach",
	)
	hasRefactorVerb := ContainsAny(
		normalized,
		"optimize",
		"optimization",
		"improve",
		"refactor",
		"cleanup",
		"clean up",
		"reorganize",
	)
	if hasStructuralImprovementRequest || hasExplicitArchitectureRequest {
		return true
	}
	return (hasScope && hasStructureTarget && hasArchitectureQuestion) ||
		(hasScope && hasStructureTarget && hasRefactorVerb) ||
		(hasScope && hasStructureTarget && ContainsAny(normalized, "identify any", "analyze", "review"))
}

func LooksLikeLocatePrompt(prompt string, normalized string) bool {
	hasLocateVerb := ContainsAny(
		normalized,
		"where is",
		"locate",
		"identify",
		"show me",
		"which code",
		"what code",
		"find the code",
		"find where",
	)
	hasVerificationGuard := ContainsAny(
		normalized,
		"identify it first",
		"show it first",
		"so i can verify",
		"before changing",
		"before you change it",
		"don't change it yet",
		"do not change it yet",
	)
	hasLiteral := PrimaryLocateLiteral(prompt) != ""
	hasUILocation := HasUILocationHint(normalized)
	if hasVerificationGuard && (hasLiteral || hasUILocation) {
		return true
	}
	return hasLocateVerb && (hasLiteral || hasUILocation)
}

func ShouldRouteUILiteralEditThroughLocate(prompt string, normalized string) bool {
	return PromptRequestsImplementation(prompt) && PrimaryLocateLiteral(prompt) != "" && HasUILocationHint(normalized)
}

func PromptRequestsRemoval(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return ContainsAny(
		normalized,
		"remove it",
		"remove this",
		"remove the",
		"delete it",
		"delete this",
		"delete the",
		"take it out",
	)
}

func LooksLikeReviewPrompt(normalized string) bool {
	return ContainsAny(
		normalized,
		"review",
		"audit",
		"bug",
		"issue",
		"problem",
		"code smell",
		"cleanup",
		"clean up",
		"refactor",
		"optimization",
		"optimize",
		"regression",
		"error",
		"identify any",
		"find one",
		"should be fixed",
	)
}

func LooksLikeIssueHuntPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return ContainsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"error",
	)
}

func LooksLikeCodeSmellPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return ContainsAny(normalized, "code smell", "cleanup", "clean up", "refactor")
}

func LooksLikeUISpacingPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return ContainsAny(normalized, "padding", "space", "spacing", "margin") &&
		ContainsAny(normalized, "under", "underneath", "below", "beneath") &&
		ContainsAny(normalized, "banner", "header")
}

func HasUILocationHint(normalized string) bool {
	return ContainsAny(
		normalized,
		"top",
		"header",
		"footer",
		"badge",
		"window",
		"chat",
		"menu",
		"status",
		"banner",
	)
}

func RequestsShellOnlyMutation(normalized string) bool {
	return ContainsAny(
		normalized,
		"shell commands only",
		"using shell commands only",
		"use shell commands only",
		"via shell only",
		"through shell only",
	)
}
