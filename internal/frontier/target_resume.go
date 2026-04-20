package frontier

import (
	"encoding/json"
	"regexp"
	"strings"

	"qwen-bubbles/internal/llama"
)

type TargetResumePlanInput struct {
	Root             string
	Prompt           string
	History          []llama.ChatMessage
	IntentTargetRefs []string
	Base             TargetResumeContext
}

type TaskResumeContext struct {
	Active                bool
	Kind                  string
	SourceLane            Lane
	ApprovedEditTargets   []string
	ReadOnlyEvidencePaths []string
	AllowedDestinations   []string
	ForbiddenPaths        []string
	AttemptedSearches     []string
	OpenGaps              []string
	VerificationPlan      []string
}

func (resume TaskResumeContext) TargetResumeContext() TargetResumeContext {
	return TargetResumeContext{
		Active:                resume.Active,
		SourceLane:            resume.SourceLane,
		ApprovedEditTargets:   append([]string(nil), resume.ApprovedEditTargets...),
		ReadOnlyEvidencePaths: append([]string(nil), resume.ReadOnlyEvidencePaths...),
		AllowedDestinations:   append([]string(nil), resume.AllowedDestinations...),
		ForbiddenPaths:        append([]string(nil), resume.ForbiddenPaths...),
		VerificationPlan:      append([]string(nil), resume.VerificationPlan...),
	}
}

type TaskMemoryResumeInput struct {
	Active              bool
	Kind                string
	Prompt              string
	ResumePrompt        string
	Lane                string
	Summary             string
	UserPrompt          string
	AttemptedSearches   []string
	OpenGaps            []string
	ChosenTargets       []string
	VerificationHistory []string
	NextActions         []string
}

type WorkflowResumeInput struct {
	Resumable           bool
	EditableTargets     []string
	TargetRefs          []string
	ReadOnlyEvidence    []string
	EvidencePaths       []string
	AllowedDestinations []string
	ForbiddenPaths      []string
	Verification        []string
	NextActions         []string
}

type DurablePlanArtifact struct {
	Version       int                      `json:"version"`
	Objective     string                   `json:"objective,omitempty"`
	SourceLane    string                   `json:"source_lane,omitempty"`
	Summary       string                   `json:"summary,omitempty"`
	ApprovalScope DurablePlanApprovalScope `json:"approval_scope"`
	PlanExit      string                   `json:"plan_exit,omitempty"`
}

type DurablePlanApprovalScope struct {
	EditableTargets     []string `json:"editable_targets,omitempty"`
	ReadOnlyEvidence    []string `json:"read_only_evidence,omitempty"`
	AllowedDestinations []string `json:"allowed_destinations,omitempty"`
	ForbiddenPaths      []string `json:"forbidden_paths,omitempty"`
	Verification        []string `json:"verification,omitempty"`
}

func BuildTargetResumeContextFromPlan(input TargetResumePlanInput) TargetResumeContext {
	resume := input.Base
	var approved []string
	var destinations []string
	addApproved := func(path string) {
		path = NormalizePath(path)
		if path == "" {
			return
		}
		approved = append(approved, path)
	}
	addDestination := func(path string) {
		path = NormalizePath(path)
		if path == "" {
			return
		}
		destinations = append(destinations, path)
	}
	for _, ref := range input.IntentTargetRefs {
		if path, ok := ResolveExistingImplementationRef(input.Root, ref); ok {
			addApproved(path)
		}
	}
	for _, ref := range RecentImplementationPlanWorkspaceRefs(input.Prompt, input.History) {
		if path, ok := ResolveExistingImplementationRef(input.Root, ref); ok {
			addApproved(path)
			continue
		}
		if ImplementationRefLooksLikeNewDestination(ref) {
			addDestination(ref)
		}
	}
	if len(approved) == 0 && len(destinations) == 0 {
		return resume
	}
	resume.Active = true
	if len(resume.ApprovedEditTargets) == 0 {
		resume.ApprovedEditTargets = LimitStrings(approved, 8)
	} else {
		resume.ApprovedEditTargets = LimitStrings(append(resume.ApprovedEditTargets, approved...), 8)
	}
	resume.AllowedDestinations = LimitStrings(append(resume.AllowedDestinations, destinations...), 8)
	resume.VerificationPlan = LimitStrings(append(resume.VerificationPlan, RecentImplementationPlanConstraintHints(input.Prompt, input.History)...), 8)
	return resume
}

func BuildTaskMemoryResumeContext(input TaskMemoryResumeInput) TaskResumeContext {
	if !taskMemoryResumeInputHasState(input) {
		return TaskResumeContext{}
	}
	approved := TaskMemoryTargetsForResume(input)
	evidence := TaskMemoryEvidencePaths(input, approved)
	return TaskResumeContext{
		Active:                true,
		Kind:                  strings.TrimSpace(input.Kind),
		SourceLane:            Lane(strings.TrimSpace(input.Lane)),
		ApprovedEditTargets:   LimitStrings(approved, 8),
		ReadOnlyEvidencePaths: LimitStrings(evidence, 16),
		AllowedDestinations:   DurablePlanAllowedDestinations(input.Summary),
		AttemptedSearches:     append([]string(nil), input.AttemptedSearches...),
		OpenGaps:              append([]string(nil), input.OpenGaps...),
		VerificationPlan:      TaskMemoryVerificationPlan(input),
	}
}

func BuildWorkflowResumeContext(input WorkflowResumeInput) TaskResumeContext {
	if !input.Resumable {
		return TaskResumeContext{}
	}
	approved := LimitStrings(firstNonEmptyStringSlice(input.EditableTargets, input.TargetRefs), 8)
	evidence := LimitStrings(append(append([]string(nil), input.ReadOnlyEvidence...), input.EvidencePaths...), 16)
	return TaskResumeContext{
		Active:                true,
		Kind:                  "workflow_plan",
		SourceLane:            LaneArchitecture,
		ApprovedEditTargets:   approved,
		ReadOnlyEvidencePaths: DurablePlanReadOnlyEvidence(evidence, approved),
		AllowedDestinations:   LimitStrings(input.AllowedDestinations, 8),
		ForbiddenPaths:        LimitStrings(input.ForbiddenPaths, 16),
		VerificationPlan:      LimitStrings(input.Verification, 8),
		OpenGaps:              UniqueNonEmptyStrings(input.NextActions),
	}
}

func TaskMemoryTargetsForResume(input TaskMemoryResumeInput) []string {
	if !taskMemoryResumeInputHasState(input) {
		return nil
	}
	if TaskMemoryDurablePlanApprovalDetected(input.Kind, input.UserPrompt) {
		if primaryTargets := DurablePlanPrimaryTargets(input.Prompt, input.Summary, input.ChosenTargets); len(primaryTargets) > 0 {
			return primaryTargets
		}
	}
	return append([]string(nil), input.ChosenTargets...)
}

func TaskMemoryEvidencePaths(input TaskMemoryResumeInput, approved []string) []string {
	if !taskMemoryResumeInputHasState(input) {
		return nil
	}
	approvedSet := make(map[string]struct{}, len(approved))
	for _, path := range approved {
		path = NormalizePath(path)
		if path != "" {
			approvedSet[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(input.AttemptedSearches)+len(input.ChosenTargets))
	add := func(path string) {
		path = NormalizePath(path)
		if path == "" {
			return
		}
		if _, ok := approvedSet[path]; ok {
			return
		}
		paths = append(paths, path)
	}
	for _, attempt := range input.AttemptedSearches {
		for _, path := range PromptMentionedWorkspacePaths(attempt) {
			add(path)
		}
	}
	for _, path := range input.ChosenTargets {
		add(path)
	}
	return LimitStrings(paths, 16)
}

func TaskMemoryVerificationPlan(input TaskMemoryResumeInput) []string {
	if !taskMemoryResumeInputHasState(input) {
		return nil
	}
	plan := make([]string, 0, len(input.VerificationHistory)+len(input.NextActions))
	for _, entry := range input.VerificationHistory {
		if strings.TrimSpace(entry) != "" {
			plan = append(plan, entry)
		}
	}
	for _, action := range input.NextActions {
		if ContainsAny(strings.ToLower(action), "verify", "test", "check") {
			plan = append(plan, action)
		}
	}
	return LimitStrings(plan, 6)
}

func TaskMemoryDurablePlanApprovalDetected(kind string, prompt string) bool {
	if strings.TrimSpace(kind) != "durable_plan" {
		return false
	}
	return LooksLikeDurablePlanApprovalPrompt(prompt) || LooksLikeTerseDurablePlanApprovalPrompt(prompt)
}

func taskMemoryResumeInputHasState(input TaskMemoryResumeInput) bool {
	return input.Active ||
		strings.TrimSpace(input.Kind) != "" ||
		strings.TrimSpace(input.Prompt) != "" ||
		strings.TrimSpace(input.ResumePrompt) != "" ||
		strings.TrimSpace(input.Summary) != "" ||
		len(input.AttemptedSearches) > 0 ||
		len(input.OpenGaps) > 0 ||
		len(input.ChosenTargets) > 0 ||
		len(input.VerificationHistory) > 0 ||
		len(input.NextActions) > 0
}

func DurablePlanPrimaryTargets(prompt string, summary string, fallbackTargets []string) []string {
	if targets := ExplicitDurablePlanTargets(summary); len(targets) > 0 {
		return LimitStrings(targets, 4)
	}
	if targets := PromptMentionedWorkspacePaths(prompt); len(targets) > 0 {
		return LimitStrings(targets, 4)
	}
	if len(fallbackTargets) == 1 {
		return LimitStrings(fallbackTargets, 1)
	}
	return nil
}

func firstNonEmptyStringSlice(candidates ...[]string) []string {
	for _, candidate := range candidates {
		if values := UniqueNonEmptyStrings(candidate); len(values) > 0 {
			return values
		}
	}
	return nil
}

func RenderDurablePlanArtifact(prompt string, lane Lane, summary string, evidencePaths []string, nextAction string) string {
	artifact := BuildDurablePlanArtifact(prompt, lane, summary, evidencePaths, nextAction)
	var b strings.Builder
	b.WriteString("Durable plan artifact\n\n")
	b.WriteString("Objective:\n")
	b.WriteString("- ")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n")
	b.WriteString("Mode:\n")
	b.WriteString("- read-only ")
	if lane != "" {
		b.WriteString(string(lane))
		b.WriteString(" ")
	}
	b.WriteString("planning; no writes until explicit approval.\n\n")
	b.WriteString("Approval scope:\n")
	b.WriteString("- editable targets: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.EditableTargets))
	b.WriteString("\n")
	b.WriteString("- read-only evidence: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.ReadOnlyEvidence))
	b.WriteString("\n")
	b.WriteString("- allowed destinations: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.AllowedDestinations))
	b.WriteString("\n")
	b.WriteString("- forbidden paths: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.ForbiddenPaths))
	b.WriteString("\n\n")
	b.WriteString("Plan artifact schema:\n")
	if data, err := json.MarshalIndent(artifact, "", "  "); err == nil {
		b.WriteString("```json\n")
		b.Write(data)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("Explore-before-plan evidence:\n")
	if paths := UniqueNormalizedPaths(evidencePaths); len(paths) > 0 {
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("- no concrete evidence paths were captured; validate targets before build mode.\n")
	}
	b.WriteString("\nProposed plan:\n")
	if strings.TrimSpace(summary) != "" {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n")
	} else {
		b.WriteString("- no model plan summary was captured.\n")
	}
	b.WriteString("\nVerification section:\n")
	b.WriteString("- identify focused tests or checks before implementation.\n")
	b.WriteString("- after implementation, report pass/fail status and exact failure output if checks fail.\n")
	b.WriteString("- do not mark the plan complete until changed files and verification evidence are named.\n\n")
	b.WriteString("Plan exit / build handoff:\n")
	b.WriteString("- wait for explicit user approval, such as \"implement this plan\", before writing.\n")
	b.WriteString("- build-switch reminder: restate the objective, constraints, target files, and verification checks before the first write.\n")
	if strings.TrimSpace(nextAction) != "" {
		b.WriteString("- next action after approval: ")
		b.WriteString(strings.TrimSpace(nextAction))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func BuildDurablePlanArtifact(prompt string, lane Lane, summary string, evidencePaths []string, nextAction string) DurablePlanArtifact {
	editable := DurablePlanPrimaryTargets(prompt, summary, nil)
	evidence := DurablePlanReadOnlyEvidence(evidencePaths, editable)
	destinations := DurablePlanCandidateDestinations(summary, editable, evidence)
	verification := []string{
		"identify focused tests or checks before implementation",
		"run verification after implementation and report pass/fail status",
	}
	if strings.TrimSpace(nextAction) != "" && ContainsAny(strings.ToLower(nextAction), "verify", "test", "check") {
		verification = append(verification, strings.TrimSpace(nextAction))
	}
	return DurablePlanArtifact{
		Version:    1,
		Objective:  strings.TrimSpace(prompt),
		SourceLane: string(lane),
		Summary:    strings.TrimSpace(summary),
		ApprovalScope: DurablePlanApprovalScope{
			EditableTargets:     LimitStrings(editable, 8),
			ReadOnlyEvidence:    LimitStrings(evidence, 16),
			AllowedDestinations: LimitStrings(destinations, 8),
			ForbiddenPaths:      nil,
			Verification:        UniqueNonEmptyStrings(verification),
		},
		PlanExit: DurablePlanExitInstruction(nextAction),
	}
}

func DurablePlanExitInstruction(nextAction string) string {
	if strings.TrimSpace(nextAction) == "" {
		return "wait for explicit user approval before implementation"
	}
	return "after explicit user approval: " + strings.TrimSpace(nextAction)
}

func joinOrNone(values []string) string {
	values = UniqueNonEmptyStrings(values)
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func ResolveExistingImplementationRef(root string, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || !LooksLikeWorkspacePathToken(strings.TrimPrefix(ref, "@")) {
		return "", false
	}
	if root == "" {
		path := NormalizePath(ref)
		if path == "" || !strings.Contains(path, "/") {
			return "", false
		}
		return path, true
	}
	for _, path := range ResolvePrimaryPaths(root, []string{ref}) {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		if WorkspaceRelativeFileExists(root, path) {
			return path, true
		}
	}
	return "", false
}

func ImplementationRefLooksLikeNewDestination(ref string) bool {
	ref = NormalizePath(strings.TrimPrefix(strings.TrimSpace(ref), "@"))
	return ref != "" && strings.Contains(ref, "/") && LooksLikeWorkspacePathToken(ref)
}

func RecentImplementationPlanWorkspaceRefs(prompt string, history []llama.ChatMessage) []string {
	if !ImplementationApprovalShouldUseRecentPlan(prompt) || len(history) == 0 {
		return nil
	}
	refs := make([]string, 0, 4)
	for i := len(history) - 1; i >= 0 && len(history)-i <= 8; i-- {
		msg := history[i]
		if normalizeHistoryRole(msg.Role) != "assistant" {
			continue
		}
		content := ImplementationPlanRelevantSection(prompt, msg.Content)
		if targets := ExplicitDurablePlanTargets(content); len(targets) > 0 {
			return LimitStrings(targets, 8)
		}
		paths := PromptMentionedWorkspacePaths(content)
		if len(paths) == 0 {
			continue
		}
		refs = append(refs, paths...)
		break
	}
	return LimitStrings(refs, 8)
}

func RecentImplementationPlanConstraintHints(prompt string, history []llama.ChatMessage) []string {
	if !ImplementationApprovalShouldUseRecentPlan(prompt) || len(history) == 0 {
		return nil
	}
	for i := len(history) - 1; i >= 0 && len(history)-i <= 8; i-- {
		msg := history[i]
		if normalizeHistoryRole(msg.Role) != "assistant" {
			continue
		}
		content := ImplementationPlanRelevantSection(prompt, msg.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		var hints []string
		if PromptForbidsDeclarationMoves(content) {
			hints = append(hints, "do not create files or move declarations; use edit_file inside the approved primary file")
		}
		if ContainsAny(strings.ToLower(content), "edit only this existing go file", "edit only this existing file", "only this existing go file", "only this existing file") {
			hints = append(hints, "single-file scope: do not create new files or widen the approved editable target")
		}
		return LimitStrings(hints, 4)
	}
	return nil
}

func DurablePlanReadOnlyEvidence(evidencePaths []string, editable []string) []string {
	editableSet := make(map[string]struct{}, len(editable))
	for _, path := range editable {
		if normalized := NormalizePath(path); normalized != "" {
			editableSet[normalized] = struct{}{}
		}
	}
	var evidence []string
	for _, path := range evidencePaths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		if _, ok := editableSet[path]; ok {
			continue
		}
		evidence = append(evidence, path)
	}
	return LimitStrings(evidence, 16)
}

func DurablePlanCandidateDestinations(summary string, editable []string, evidence []string) []string {
	blocked := make(map[string]struct{}, len(editable)+len(evidence))
	for _, path := range append(append([]string(nil), editable...), evidence...) {
		if normalized := NormalizePath(path); normalized != "" {
			blocked[normalized] = struct{}{}
		}
	}
	destinations := make([]string, 0, 4)
	addPaths := func(text string) {
		for _, path := range PromptMentionedWorkspacePaths(text) {
			path = NormalizePath(path)
			if path == "" {
				continue
			}
			if _, ok := blocked[path]; ok {
				continue
			}
			destinations = append(destinations, path)
		}
	}
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, prefix := range []string{"allowed destinations:", "plan-approved new destinations:", "new destinations:"} {
			if strings.HasPrefix(lower, prefix) {
				_, tail, _ := strings.Cut(trimmed, ":")
				addPaths(tail)
				continue
			}
		}
		if idx := strings.Index(lower, " into "); idx >= 0 && ContainsAny(lower[:idx], "split", "extract", "move", "separate") {
			addPaths(trimmed[idx+len(" into "):])
			continue
		}
		if ContainsAny(lower, "create new file", "add new file", "new helper file", "new destination file") {
			addPaths(trimmed)
		}
	}
	return LimitStrings(destinations, 8)
}

func DurablePlanAllowedDestinations(summary string) []string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	if destinations := durablePlanArtifactAllowedDestinations(summary); len(destinations) > 0 {
		return LimitStrings(destinations, 8)
	}
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "allowed destinations:") && !strings.HasPrefix(lower, "plan-approved new destinations:") {
			continue
		}
		_, tail, _ := strings.Cut(trimmed, ":")
		return LimitStrings(PromptMentionedWorkspacePaths(tail), 8)
	}
	return nil
}

func durablePlanArtifactAllowedDestinations(summary string) []string {
	start := strings.Index(summary, "```json")
	if start < 0 {
		return nil
	}
	rest := summary[start+len("```json"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return nil
	}
	var artifact struct {
		ApprovalScope struct {
			AllowedDestinations []string `json:"allowed_destinations,omitempty"`
		} `json:"approval_scope"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &artifact); err != nil {
		return nil
	}
	return LimitStrings(artifact.ApprovalScope.AllowedDestinations, 8)
}

func ImplementationApprovalShouldUseRecentPlan(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return LooksLikeEllipticalImplementationFollowup(normalized) ||
		LooksLikeDurablePlanApprovalPrompt(normalized) ||
		LooksLikeTerseDurablePlanApprovalPrompt(normalized) ||
		(ContainsAny(normalized, "go ahead", "proceed", "begin", "start", "do phase", "phase 1", "phase one") &&
			ContainsAny(normalized, "phase", "do", "implement", "extract", "refactor"))
}

func ImplementationPlanRelevantSection(prompt string, content string) string {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	phase := ""
	for _, match := range regexp.MustCompile(`(?i)\bphase\s+([0-9]+|one|two|three|four|five)\b`).FindAllStringSubmatch(normalizedPrompt, -1) {
		if len(match) == 2 {
			phase = strings.ToLower(match[1])
			break
		}
	}
	if phase == "" {
		return content
	}
	normalizedContent := strings.ToLower(content)
	candidates := []string{"phase " + phase}
	switch phase {
	case "1":
		candidates = append(candidates, "phase one")
	case "2":
		candidates = append(candidates, "phase two")
	case "3":
		candidates = append(candidates, "phase three")
	case "one":
		candidates = append(candidates, "phase 1")
	case "two":
		candidates = append(candidates, "phase 2")
	case "three":
		candidates = append(candidates, "phase 3")
	}
	start := -1
	for _, candidate := range candidates {
		if idx := strings.Index(normalizedContent, candidate); idx >= 0 && (start < 0 || idx < start) {
			start = idx
		}
	}
	if start < 0 {
		return content
	}
	rest := content[start:]
	lowerRest := strings.ToLower(rest)
	end := len(rest)
	for _, marker := range []string{"\n**phase ", "\nphase "} {
		if idx := strings.Index(lowerRest[1:], marker); idx >= 0 && idx+1 < end {
			end = idx + 1
		}
	}
	return rest[:end]
}

func ExplicitDurablePlanTargets(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	targets := make([]string, 0, 2)
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "target:") && !strings.HasPrefix(lower, "target file:") && !strings.HasPrefix(lower, "target files:") {
			continue
		}
		targets = append(targets, PromptMentionedWorkspacePaths(trimmed)...)
	}
	return LimitStrings(targets, 4)
}

func LooksLikeDurablePlanApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	if ContainsAny(normalized, "don't", "do not", "not yet", "hold off", "wait", "without performing", "without applying") {
		return false
	}
	return ContainsAny(normalized,
		"implement this plan",
		"implement the plan",
		"approve this plan",
		"approved, implement",
		"build this plan",
		"begin this plan",
		"begin the plan",
		"start this plan",
		"start the plan",
		"apply this plan",
		"apply the plan",
		"proceed with this plan",
		"go ahead with the plan",
		"perform the refactor",
		"perform this refactor",
		"perform that refactor",
		"execute the refactor",
		"execute this refactor",
		"execute that refactor",
		"apply the refactor",
		"apply this refactor",
		"apply that refactor",
		"do the refactor",
		"start the refactor",
		"begin the refactor",
		"commence the refactor",
		"perform the approved plan",
		"execute the approved plan",
		"build the approved plan",
	)
}

func LooksLikeTerseDurablePlanApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.ReplaceAll(normalized, "’", "'")
	normalized = strings.Trim(normalized, " \t\r\n.!?")
	if normalized == "" {
		return false
	}
	switch normalized {
	case "do it",
		"please do it",
		"go ahead",
		"please go ahead",
		"proceed",
		"please proceed",
		"start",
		"please start",
		"begin",
		"please begin",
		"commence",
		"please commence",
		"let's commence",
		"lets commence",
		"build it",
		"please build it",
		"perform it",
		"yes",
		"yes please",
		"approved",
		"looks good",
		"ship it":
		return true
	default:
		return ContainsAny(normalized,
			"let's do it",
			"lets do it",
			"let us do it",
			"please begin",
			"please start",
			"please proceed",
			"please go ahead",
			"go ahead",
			"ship it",
		) && !ContainsAny(normalized, "don't", "do not", "not yet", "hold off", "wait")
	}
}

func LooksLikeEllipticalImplementationFollowup(normalized string) bool {
	switch strings.Trim(normalized, ".! ") {
	case "do it",
		"please do",
		"please do it",
		"go ahead",
		"go ahead and do it",
		"do this",
		"do that",
		"make it happen",
		"proceed",
		"begin",
		"start",
		"implement it",
		"apply it",
		"add it",
		"add it all",
		"add all of it",
		"implement all of it",
		"implement that",
		"implement those changes",
		"perform the refactor",
		"perform this refactor",
		"perform that refactor",
		"execute the refactor",
		"execute this refactor",
		"execute that refactor",
		"apply the refactor",
		"apply this refactor",
		"apply that refactor",
		"do the refactor",
		"make the change",
		"make those changes":
		return true
	default:
		return false
	}
}

func normalizeHistoryRole(role string) string {
	switch role {
	case "user", "assistant", "system":
		return role
	case "tool":
		return "system"
	default:
		return "system"
	}
}
