package app

import (
	"fmt"
	"strings"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/implementationdispatch"
)

type planExecuteScopeIntake struct {
	RequiredTargets    []string
	AllowedTargets     []string
	ForbiddenPaths     []string
	AuthorizedNewFiles []string
	ClosedScope        bool
	ExpansionPolicy    plancontract.ScopeExpansionPolicy
	PathRoles          []plancontract.ScopeExpansionPath
}

func classifyPlanExecuteScopeIntake(prompt string, plan promptExecutionPlan) planExecuteScopeIntake {
	return implementationDispatchCompilePlanExecuteScopeIntake(prompt, plan, config.Config{})
}

func implementationDispatchCompilePlanExecuteScopeIntake(prompt string, plan promptExecutionPlan, _ config.Config) planExecuteScopeIntake {
	intake := planExecuteScopeIntake{
		RequiredTargets:    appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...),
		AllowedTargets:     plancontract.AllowedEditPathsFromPrompt(prompt),
		AuthorizedNewFiles: appendUniqueBriefStrings(nil, plan.runtimeBrief.AuthorizedNewFiles...),
		ClosedScope:        implementationDispatchPromptClosesScope(prompt),
	}

	intake.RequiredTargets = appendUniqueBriefStrings(intake.RequiredTargets, plancontract.RequiredEditPathsFromPrompt(prompt)...)

	if strings.TrimSpace(plan.route.Reason) == "typed_plan_execute_command" {
		directTargets := implementationDispatchPromptMutationTargets(prompt)
		intake.RequiredTargets = appendUniqueBriefStrings(intake.RequiredTargets, directTargets...)
	}

	intake.ExpansionPolicy = plancontract.ScopeExpansionPolicyBoundedDiscovery
	if intake.ClosedScope {
		intake.ExpansionPolicy = plancontract.ScopeExpansionPolicyClosed
	}

	intake.PathRoles = implementationDispatchScopePathRoles(prompt, intake)
	return intake
}

func implementationDispatchPromptClosesScope(prompt string) bool {
	text := plannerWordText(prompt)
	for _, phrase := range []string{
		" do not edit any other files ",
		" do not edit other files ",
		" don't edit any other files ",
		" don't edit other files ",
		" no other files ",
		" only edit ",
		" edit only ",
		" only modify ",
		" modify only ",
		" only change ",
		" change only ",
		" limit edits to ",
		" restrict edits to ",
		" limit changes to ",
		" restrict changes to ",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

func implementationDispatchPromptMutationTargets(prompt string) []string {
	var targets []string
	for _, path := range pathparse.FilterWorkspaceFilePaths(pathparse.MentionedWorkspacePaths(prompt)) {
		if implementationDispatchPromptPathRole(prompt, path) == plancontract.ScopePathRoleMutationTarget {
			targets = appendUniqueBriefStrings(targets, path)
		}
	}

	return targets
}

func implementationDispatchPromptPathRole(prompt string, path string) plancontract.ScopePathRole {
	normalized, ok := pathparse.NormalizeWorkspacePathToken(path)
	if !ok {
		return ""
	}

	if planExecutePromptPathIsMutationTarget(prompt, normalized) {
		return plancontract.ScopePathRoleMutationTarget
	}

	segment := implementationDispatchPromptSegmentForPath(prompt, normalized)
	words := plannerWordText(segment)
	pathWords := strings.TrimSpace(plannerWordText(normalized))
	switch {
	case implementationDispatchSegmentMarksReadOnlyEvidence(words, pathWords):
		return plancontract.ScopePathRoleReadOnlyEvidence
	case implementationDispatchSegmentMarksReferenceDocument(words, normalized, pathWords):
		return plancontract.ScopePathRoleReferenceDocument
	default:
		return ""
	}
}

func implementationDispatchPromptSegmentForPath(prompt string, path string) string {
	lowerPrompt := strings.ToLower(prompt)
	lowerPath := strings.ToLower(path)
	idx := strings.Index(lowerPrompt, lowerPath)
	if idx < 0 {
		return prompt
	}

	start := idx - 96
	if start < 0 {
		start = 0
	}
	end := idx + len(path) + 96
	if end > len(prompt) {
		end = len(prompt)
	}

	return prompt[start:end]
}

func implementationDispatchSegmentMarksReadOnlyEvidence(words string, pathWords string) bool {
	if pathWords == "" || !strings.Contains(words, " "+pathWords+" ") {
		return false
	}

	for _, phrase := range []string{
		" use " + pathWords + " as evidence ",
		" read " + pathWords + " ",
		" inspect " + pathWords + " ",
		" look at " + pathWords + " ",
		" " + pathWords + " as evidence ",
		" " + pathWords + " evidence only ",
		" " + pathWords + " read only ",
	} {
		if strings.Contains(words, phrase) {
			return true
		}
	}

	return false
}

func implementationDispatchSegmentMarksReferenceDocument(words string, path string, pathWords string) bool {
	if !implementationdispatch.LooksLikePlanDocument(path) {
		return false
	}
	if pathWords == "" || !strings.Contains(words, " "+pathWords+" ") {
		return false
	}

	for _, phrase := range []string{
		" implement from " + pathWords + " ",
		" work from " + pathWords + " ",
		" use " + pathWords + " ",
		" using " + pathWords + " ",
		" based on " + pathWords + " ",
		" reference " + pathWords + " ",
	} {
		if strings.Contains(words, phrase) {
			return true
		}
	}

	return false
}

func planExecutePromptPathIsMutationTarget(prompt string, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	for _, line := range strings.Split(prompt, "\n") {
		remaining := line
		offset := 0
		for {
			index := strings.Index(remaining, path)
			if index < 0 {
				break
			}

			before := line[:offset+index]
			if planExecutePromptPrefixHasMutationVerb(before) {
				return true
			}

			offset += index + len(path)
			remaining = line[offset:]
		}
	}

	return false
}

func planExecutePromptPrefixHasMutationVerb(prefix string) bool {
	tokens := planExecuteScopeIntakeTokens(prefix)
	if len(tokens) == 0 {
		return false
	}

	lastMutation := -1
	for i, token := range tokens {
		if planExecuteScopeIntakeIsMutationVerb(token) {
			lastMutation = i
		}
	}

	if lastMutation < 0 || len(tokens)-lastMutation > 5 {
		return false
	}

	return !planExecuteScopeIntakeHasNegation(tokens, lastMutation)
}

func planExecuteScopeIntakeTokens(text string) []string {
	text = strings.ToLower(text)
	replacer := strings.NewReplacer(
		"/", " ",
		"`", " ",
		"'", " ",
		"\"", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		",", " ",
		".", " ",
		":", " ",
		";", " ",
		"-", " ",
	)

	return strings.Fields(replacer.Replace(text))
}

func planExecuteScopeIntakeIsMutationVerb(token string) bool {
	switch strings.TrimSpace(token) {
	case "add", "added", "adding",
		"append", "appended", "appending",
		"change", "changed", "changing",
		"create", "created", "creating",
		"edit", "edited", "editing",
		"modify", "modified", "modifying",
		"rewrite", "rewriting",
		"touch", "touched", "touching",
		"update", "updated", "updating",
		"write", "writing":
		return true
	default:
		return false
	}
}

func planExecuteScopeIntakeHasNegation(tokens []string, verbIndex int) bool {
	start := verbIndex - 3
	if start < 0 {
		start = 0
	}

	for _, token := range tokens[start:verbIndex] {
		switch token {
		case "avoid", "don't", "dont", "never", "no", "not", "without":
			return true
		}
	}

	return verbIndex >= 1 && tokens[verbIndex-1] == "do"
}

func implementationDispatchScopePathRoles(prompt string, intake planExecuteScopeIntake) []plancontract.ScopeExpansionPath {
	var records []plancontract.ScopeExpansionPath
	for _, path := range intake.RequiredTargets {
		records = append(records, plancontract.ScopeExpansionPath{Path: path, Role: plancontract.ScopePathRoleMutationTarget, Source: "tui_plan_execute_intake"})
	}
	for _, path := range intake.AllowedTargets {
		records = append(records, plancontract.ScopeExpansionPath{Path: path, Role: plancontract.ScopePathRoleCandidateExpansion, Source: "tui_plan_execute_intake"})
	}
	for _, path := range pathparse.FilterWorkspaceFilePaths(pathparse.MentionedWorkspacePaths(prompt)) {
		role := implementationDispatchPromptPathRole(prompt, path)
		if role == "" {
			continue
		}
		records = append(records, plancontract.ScopeExpansionPath{Path: path, Role: role, Source: "prompt"})
	}

	event := plancontract.ScopeExpansionEvent{Kind: plancontract.ScopeExpansionEventProposalCreated, Policy: intake.ExpansionPolicy, Paths: records}.Normalized()
	return event.Paths
}

func implementationDispatchCandidateExpansionRecords(paths []string, source string) []plancontract.ScopeExpansionPath {
	var records []plancontract.ScopeExpansionPath
	source = strings.TrimSpace(source)

	for _, path := range paths {
		records = append(records, plancontract.ScopeExpansionPath{
			Path:   path,
			Role:   plancontract.ScopePathRoleCandidateExpansion,
			Source: source,
		})
	}

	return plancontract.NormalizeScopeExpansionPaths(records)
}

func implementationDispatchFormatScopeExpansionEvent(event plancontract.ScopeExpansionEvent) string {
	event = event.Normalized()
	var paths []string
	for _, path := range event.Paths {
		paths = append(paths, fmt.Sprintf("%s:%s", path.Role, path.Path))
	}
	return fmt.Sprintf("%s policy=%s source=%s paths=%s reason=%s", event.Kind, event.Policy, event.Source, strings.Join(paths, ","), event.Reason)
}
