package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/workflows/featureplanner"
)

func (m *Model) saveFeaturePlannerStateAndPlanTab(state *featureplanner.FeatureState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("feature planner state is nil")
	}

	root := m.projectRoot()
	statePath, err := featureplanner.SaveState(root, state)
	if err != nil {
		return "", err
	}

	planPath, err := m.writeFeaturePlannerPlanTabArtifact(*state, statePath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(planPath) == "" {
		return statePath, nil
	}

	state.PlanPath = planPath

	return featureplanner.SaveState(root, state)
}

func (m *Model) writeFeaturePlannerPlanTabArtifact(state featureplanner.FeatureState, statePath string) (string, error) {
	plan, candidate, ok := featurePlannerPlanTabCandidate(state)
	if !ok {
		return "", nil
	}

	path := m.featurePlannerPlanTabPath(state.SessionID)
	if strings.TrimSpace(path) == "" {
		return "", nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir feature planner plan tab directory: %w", err)
	}

	body := renderFeaturePlannerPlanTabMarkdown(state, statePath, plan, candidate)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write feature planner plan tab artifact: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("replace feature planner plan tab artifact: %w", err)
	}

	m.planMTime = time.Time{}
	m.planContent = ""
	m.planLoadError = ""
	m.renderPlanPanelContent()
	m.logFeaturePlannerInternalEvent("idiom.feature_planner.plan_tab_updated", featurePlannerDisplayPath(m.projectRoot(), path))

	return featurePlannerRelPath(m.projectRoot(), path), nil
}

func (m *Model) featurePlannerPlanTabPath(sessionID string) string {
	if m != nil && m.workspace != nil && strings.TrimSpace(m.debugLogPath) != "" {
		if path := strings.TrimSpace(m.ensurePlanPath()); path != "" {
			return path
		}
	}

	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		return ""
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "feature-planner"
	}

	return filepath.Join(root, planDirName, sessionID+".md")
}

func featurePlannerPlanTabCandidate(state featureplanner.FeatureState) (featureplanner.FeaturePlan, featureplanner.RejectedPlanCandidate, bool) {
	if featurePlannerPlanHasDisplayableContent(state.Plan) && !featurePlannerStatePrefersRejectedCandidate(state.Status) {
		return state.Plan, featureplanner.RejectedPlanCandidate{}, true
	}

	if len(state.RejectedPlanCandidates) > 0 {
		candidate := state.RejectedPlanCandidates[len(state.RejectedPlanCandidates)-1]
		if featurePlannerPlanHasDisplayableContent(candidate.Plan) {
			return candidate.Plan, candidate, true
		}
	}

	if featurePlannerPlanHasDisplayableContent(state.Plan) {
		return state.Plan, featureplanner.RejectedPlanCandidate{}, true
	}

	return featureplanner.FeaturePlan{}, featureplanner.RejectedPlanCandidate{}, false
}

func featurePlannerStatePrefersRejectedCandidate(status string) bool {
	switch strings.TrimSpace(status) {
	case featureplanner.FeatureStatePlanningQuestion,
		featureplanner.FeatureStatePlanningRepair,
		featureplanner.FeatureStatePlanningStuck,
		featureplanner.FeatureStateStaleEditAnchor,
		featureplanner.FeatureStateMalformedOutput:
		return true
	default:
		return false
	}
}

func featurePlannerPlanHasDisplayableContent(plan featureplanner.FeaturePlan) bool {
	return strings.TrimSpace(plan.Summary) != "" ||
		strings.TrimSpace(plan.TopLevelScope.Objective) != "" ||
		len(plan.Atoms) > 0
}

func renderFeaturePlannerPlanTabMarkdown(state featureplanner.FeatureState, statePath string, plan featureplanner.FeaturePlan, candidate featureplanner.RejectedPlanCandidate) string {
	var b strings.Builder

	b.WriteString("# Feature Planner Candidate\n\n")
	writeFeaturePlannerMarkdownField(&b, "Status", state.Status)
	writeFeaturePlannerMarkdownField(&b, "Session", state.SessionID)
	writeFeaturePlannerMarkdownField(&b, "State", statePath)
	if hash := strings.TrimSpace(candidate.PlanHash); hash != "" {
		writeFeaturePlannerMarkdownField(&b, "Candidate", hash)
	}
	if diagnostic := strings.TrimSpace(candidate.Diagnostic); diagnostic != "" {
		writeFeaturePlannerMarkdownField(&b, "Diagnostic", diagnostic)
	}
	b.WriteString("\n")

	writeFeaturePlannerMarkdownSection(&b, "Objective", firstNonEmptyFeaturePlannerPlanText(plan.TopLevelScope.Objective, plan.SourceTask.Summary, state.PromptSummary, plan.Summary))
	writeFeaturePlannerMarkdownSection(&b, "Summary", plan.Summary)
	writeFeaturePlannerMarkdownList(&b, "Success Criteria", plan.TopLevelScope.SuccessCriteria)
	writeFeaturePlannerMarkdownList(&b, "Non Goals", plan.TopLevelScope.NonGoals)
	writeFeaturePlannerMarkdownList(&b, "Proposed Paths", featurePlannerPlanTabPaths(plan))
	writeFeaturePlannerMarkdownVerification(&b, plan)
	writeFeaturePlannerMarkdownQuestions(&b, state.OpenQuestions)
	writeFeaturePlannerMarkdownAtoms(&b, plan.Atoms)

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeFeaturePlannerMarkdownField(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}

	fmt.Fprintf(b, "- **%s:** %s\n", label, featurePlannerInlineMarkdown(value))
}

func writeFeaturePlannerMarkdownSection(b *strings.Builder, title string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}

	fmt.Fprintf(b, "## %s\n\n%s\n\n", title, value)
}

func writeFeaturePlannerMarkdownList(b *strings.Builder, title string, values []string) {
	values = featurePlannerUniqueStrings(values)
	if len(values) == 0 {
		return
	}

	fmt.Fprintf(b, "## %s\n\n", title)
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
	b.WriteString("\n")
}

func writeFeaturePlannerMarkdownVerification(b *strings.Builder, plan featureplanner.FeaturePlan) {
	var values []string
	for _, obligation := range plan.TDDContract.Obligations {
		command := strings.TrimSpace(obligation.Command)
		if command == "" {
			continue
		}
		label := strings.TrimSpace(obligation.ID)
		if label == "" {
			label = "verification"
		}
		values = append(values, label+": "+command)
	}
	for _, atom := range plan.Atoms {
		values = append(values, atom.TestFirst.Commands...)
		values = append(values, atom.RequiredTests...)
	}

	writeFeaturePlannerMarkdownList(b, "Verification", values)
}

func writeFeaturePlannerMarkdownQuestions(b *strings.Builder, questions []featureplanner.PlannerQuestion) {
	var values []string
	for _, question := range questions {
		if !featurePlannerQuestionOpen(question) {
			continue
		}
		summary := strings.TrimSpace(question.QuestionSummary)
		decision := strings.TrimSpace(question.RequestedDecision)
		switch {
		case summary != "" && decision != "":
			values = append(values, summary+" - "+decision)
		case summary != "":
			values = append(values, summary)
		case decision != "":
			values = append(values, decision)
		}
	}

	writeFeaturePlannerMarkdownList(b, "Open Questions", values)
}

func writeFeaturePlannerMarkdownAtoms(b *strings.Builder, atoms []featureplanner.FeatureAtomPlan) {
	if len(atoms) == 0 {
		return
	}

	b.WriteString("## Atoms\n\n")
	for _, atom := range atoms {
		title := firstNonEmptyFeaturePlannerPlanText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		fmt.Fprintf(b, "- [ ] %s\n", title)
		if summary := strings.TrimSpace(atom.Summary); summary != "" {
			fmt.Fprintf(b, "  - Summary: %s\n", summary)
		}
		if paths := featurePlannerUniqueStrings(atom.RequiredEditPaths); len(paths) > 0 {
			fmt.Fprintf(b, "  - Required paths: %s\n", strings.Join(paths, ", "))
		}
		if tests := featurePlannerUniqueStrings(append(append([]string(nil), atom.TestFirst.Commands...), atom.RequiredTests...)); len(tests) > 0 {
			fmt.Fprintf(b, "  - Tests: %s\n", strings.Join(tests, ", "))
		}
	}
	b.WriteString("\n")
}

func featurePlannerPlanTabPaths(plan featureplanner.FeaturePlan) []string {
	var paths []string
	for _, atom := range plan.Atoms {
		paths = append(paths, atom.RequiredEditPaths...)
		paths = append(paths, atom.AllowedEditPaths...)
		paths = append(paths, atom.TestFirst.TestArtifactPaths...)
	}

	return featurePlannerUniqueStrings(paths)
}

func featurePlannerUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func firstNonEmptyFeaturePlannerPlanText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func featurePlannerInlineMarkdown(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
}

func featurePlannerRelPath(root string, path string) string {
	root = strings.TrimSpace(root)
	path = strings.TrimSpace(path)
	if root == "" || path == "" {
		return path
	}

	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}

	return filepath.ToSlash(rel)
}

func featurePlannerDisplayPath(root string, path string) string {
	display := featurePlannerRelPath(root, path)
	if display == "" {
		return path
	}

	return display
}
