package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
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
	m.logFeaturePlannerInternalEvent(featureplannercontract.EventName(featureplannercontract.EventPlanTabUpdated), featurePlannerDisplayPath(m.projectRoot(), path))

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
	candidate, ok := featureplanner.SelectFeaturePlanProjectionCandidate(state)
	return candidate.Plan, candidate.RejectedCandidate, ok
}

func renderFeaturePlannerPlanTabMarkdown(state featureplanner.FeatureState, statePath string, plan featureplanner.FeaturePlan, candidate featureplanner.RejectedPlanCandidate) string {
	var b strings.Builder
	review := featureplanner.FeaturePlanTraceabilityReviewForPlan(plan)

	b.WriteString("# Feature Planner Candidate\n\n")
	writeFeaturePlannerMarkdownField(&b, "Status", state.Status)
	writeFeaturePlannerMarkdownField(&b, "Session", state.SessionID)
	writeFeaturePlannerMarkdownField(&b, "State", statePath)
	if hash := strings.TrimSpace(candidate.PlanHash); hash != "" {
		writeFeaturePlannerMarkdownField(&b, "Candidate", hash)
	}
	writeFeaturePlannerPlanTabArtifactFields(&b, plan)
	b.WriteString("\n")

	writeFeaturePlannerProposedFiles(&b, plan)
	writeFeaturePlannerCoverageReviewSummary(&b, review)
	writeFeaturePlannerBehaviorReview(&b, review)
	writeFeaturePlannerTraceabilityMatrix(&b, review)
	writeFeaturePlannerMarkdownCoverageSections(&b, state, statePath, plan, candidate)
	writeFeaturePlannerMarkdownAtoms(&b, plan.Atoms)
	writeFeaturePlannerPlanTabVerificationClosures(&b, featurePlannerVerificationClosuresForPlan(plan, state.VerificationClosures))
	if release, ok := featurePlannerReleaseReadinessForPlan(plan, state.ReleaseReadiness); ok {
		writeFeaturePlannerPlanTabReleaseReadiness(&b, release)
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeFeaturePlannerCoverageReviewSummary(b *strings.Builder, review featureplanner.FeaturePlanTraceabilityReview) {
	missingConcreteTests := 0
	for _, behavior := range review.Behaviors {
		if behavior.MissingTests {
			missingConcreteTests++
		}
	}
	inScopeSurfaces := 0
	missingSurfaceObligations := 0
	openSurfaceQuestions := 0
	for _, surface := range review.Surfaces {
		if surface.Status == "accepted" {
			inScopeSurfaces++
		}
		if surface.MissingObligations {
			missingSurfaceObligations++
		}
		if surface.OpenQuestion {
			openSurfaceQuestions++
		}
	}

	b.WriteString("## Coverage Review Summary\n\n")
	fmt.Fprintf(b, "- Behaviors: %d\n", len(review.Behaviors))
	fmt.Fprintf(b, "- Behavior surfaces: %d\n", len(review.Surfaces))
	fmt.Fprintf(b, "- In-scope surfaces: %d\n", inScopeSurfaces)
	fmt.Fprintf(b, "- In-scope surfaces missing TDD obligations: %d\n", missingSurfaceObligations)
	fmt.Fprintf(b, "- Open surface questions: %d\n", openSurfaceQuestions)
	fmt.Fprintf(b, "- Variants: %d\n", review.VariantCount)
	fmt.Fprintf(b, "- Domain invariants: %d\n", len(review.InvariantRefs))
	fmt.Fprintf(b, "- Concrete test cases: %d\n", len(review.TestCaseRefs))
	fmt.Fprintf(b, "- Test files: %d\n", len(review.TestFiles))
	fmt.Fprintf(b, "- Required commands: %d\n", len(review.Commands))
	fmt.Fprintf(b, "- Behaviors missing concrete test cases: %d\n\n", missingConcreteTests)
}

func writeFeaturePlannerBehaviorReview(b *strings.Builder, review featureplanner.FeaturePlanTraceabilityReview) {
	b.WriteString("## Behavior Review\n\n")
	if len(review.Behaviors) == 0 {
		b.WriteString("- No behavior obligations recorded in the structured plan.\n\n")
		return
	}

	for _, behavior := range review.Behaviors {
		title := firstNonEmptyFeaturePlannerPlanText(behavior.ID, behavior.Behavior, "unnamed behavior")
		if behavior.ID != "" && behavior.Behavior != "" && behavior.ID != behavior.Behavior {
			title = behavior.ID + " - " + behavior.Behavior
		}
		fmt.Fprintf(b, "### Behavior: %s\n\n", title)
		writeFeaturePlannerBehaviorList(b, "Atoms", behavior.AtomTitles, "No atom linkage recorded.")
		writeFeaturePlannerBehaviorList(b, "Requirements", behavior.Requirements, "No requirement refs recorded.")
		writeFeaturePlannerBehaviorList(b, "Acceptance refs", behavior.Acceptance, "No acceptance refs recorded.")
		writeFeaturePlannerBehaviorList(b, "Scenarios", behavior.Scenarios, "No BDD scenarios recorded.")
		writeFeaturePlannerBehaviorList(b, "Variants", behavior.Variants, "No variants recorded.")
		writeFeaturePlannerBehaviorList(b, "Invariants", behavior.Invariants, "No domain invariants recorded.")
		writeFeaturePlannerBehaviorList(b, "Concrete tests", behavior.TestCases, "No concrete test cases recorded.")
		writeFeaturePlannerBehaviorList(b, "Test files", behavior.TestFiles, "No test files recorded.")
		writeFeaturePlannerBehaviorList(b, "Commands", behavior.Commands, "No commands recorded.")
		writeFeaturePlannerBehaviorList(b, "Surfaces", behavior.Surfaces, "No behavior surface linkage recorded.")
		b.WriteString("\n")
	}
}

func writeFeaturePlannerBehaviorList(b *strings.Builder, label string, values []string, empty string) {
	values = featurePlannerUniqueStrings(values)
	if len(values) == 0 {
		fmt.Fprintf(b, "- %s: %s\n", label, empty)
		return
	}
	fmt.Fprintf(b, "- %s: %s\n", label, strings.Join(values, "; "))
}

func writeFeaturePlannerTraceabilityMatrix(b *strings.Builder, review featureplanner.FeaturePlanTraceabilityReview) {
	b.WriteString("## Traceability Matrix\n\n")
	if len(review.Behaviors) == 0 {
		b.WriteString("- No behavior obligations recorded in the structured plan.\n\n")
		return
	}
	b.WriteString("| Behavior | Acceptance | Scenarios / variants | Invariants | Concrete tests | Test files | Commands | Atoms |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, behavior := range review.Behaviors {
		label := firstNonEmptyFeaturePlannerPlanText(behavior.ID, behavior.Behavior, "unnamed behavior")
		fmt.Fprintf(
			b,
			"| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			featurePlannerMarkdownTableCell(label),
			featurePlannerMarkdownTableListCell(behavior.Acceptance),
			featurePlannerMarkdownTableListCell(featurePlannerUniqueStrings(append(append([]string(nil), behavior.Scenarios...), behavior.Variants...))),
			featurePlannerMarkdownTableListCell(behavior.Invariants),
			featurePlannerMarkdownTableListCell(behavior.TestCases),
			featurePlannerMarkdownTableListCell(behavior.TestFiles),
			featurePlannerMarkdownTableListCell(behavior.Commands),
			featurePlannerMarkdownTableListCell(behavior.AtomTitles),
		)
	}
	b.WriteString("\n")
}

func featurePlannerMarkdownTableListCell(values []string) string {
	values = featurePlannerUniqueStrings(values)
	if len(values) == 0 {
		return " "
	}
	for i, value := range values {
		values[i] = featurePlannerInlineMarkdown(value)
		values[i] = strings.ReplaceAll(values[i], "|", "\\|")
	}
	return strings.Join(values, "<br>")
}

func featurePlannerMarkdownTableCell(value string) string {
	value = featurePlannerInlineMarkdown(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return " "
	}
	return value
}

func writeFeaturePlannerPlanTabArtifactFields(b *strings.Builder, plan featureplanner.FeaturePlan) {
	writeFeaturePlannerMarkdownField(b, "Artifact", plan.Artifact.ArtifactID)
	if plan.Artifact.Revision > 0 {
		writeFeaturePlannerMarkdownField(b, "Revision", fmt.Sprintf("%d", plan.Artifact.Revision))
	}
	writeFeaturePlannerMarkdownField(b, "Digest", plan.Artifact.Digest)
	writeFeaturePlannerMarkdownField(b, "Workspace", plan.Artifact.WorkspaceFingerprint)
	writeFeaturePlannerMarkdownField(b, "Approval", plan.Approval.Status)
	writeFeaturePlannerMarkdownField(b, "Approval mode", plan.Approval.Mode)
	writeFeaturePlannerMarkdownField(b, "Approval event", plan.Approval.EventID)
}

func writeFeaturePlannerProposedFiles(b *strings.Builder, plan featureplanner.FeaturePlan) {
	required, allowed, testArtifacts := featurePlannerProposedFileGroups(plan)
	b.WriteString("## Proposed Files\n\n")
	if len(required) == 0 && len(allowed) == 0 && len(testArtifacts) == 0 {
		b.WriteString("- No proposed edit files recorded in the structured plan.\n\n")
		return
	}

	writeFeaturePlannerProposedFileGroup(b, "Required edit paths", required)
	writeFeaturePlannerProposedFileGroup(b, "Allowed candidate edit paths", allowed)
	writeFeaturePlannerProposedFileGroup(b, "Test artifact paths", testArtifacts)
	b.WriteString("\n")
}

func featurePlannerProposedFileGroups(plan featureplanner.FeaturePlan) ([]string, []string, []string) {
	groups := featureplanner.FeaturePlanProposedFiles(plan)
	return groups.RequiredEditPaths, groups.AllowedEditPaths, groups.TestArtifactPaths
}

func writeFeaturePlannerProposedFileGroup(b *strings.Builder, label string, paths []string) {
	paths = featurePlannerUniqueStrings(paths)
	if len(paths) == 0 {
		return
	}

	fmt.Fprintf(b, "- %s (%d):\n", label, len(paths))
	for _, path := range paths {
		fmt.Fprintf(b, "  - %s\n", featurePlannerInlineMarkdown(path))
	}
}

func writeFeaturePlannerPlanTabVerificationClosures(b *strings.Builder, closures []featureplanner.VerificationClosure) {
	if len(closures) == 0 {
		return
	}

	b.WriteString("## Verification Closures\n\n")
	for _, closure := range closures {
		title := firstNonEmptyFeaturePlannerPlanText(closure.AtomID, closure.EventID, "atom")
		fmt.Fprintf(b, "- %s: %s", title, featurePlannerInlineMarkdown(string(closure.Status)))
		if closure.PlanRevision > 0 {
			fmt.Fprintf(b, " (plan r%d)", closure.PlanRevision)
		}
		b.WriteString("\n")
		if closure.EventID != "" {
			fmt.Fprintf(b, "  - Closure: %s\n", featurePlannerInlineMarkdown(closure.EventID))
		}
		if closure.PlanDigest != "" {
			fmt.Fprintf(b, "  - Digest: %s\n", featurePlannerInlineMarkdown(closure.PlanDigest))
		}
		if len(closure.ChangedPaths) > 0 {
			fmt.Fprintf(b, "  - Changed paths: %s\n", strings.Join(featurePlannerUniqueStrings(closure.ChangedPaths), ", "))
		}
		if len(closure.SourceSatisfiedEvidencePaths) > 0 {
			fmt.Fprintf(b, "  - Source-satisfied paths: %s\n", strings.Join(featurePlannerUniqueStrings(closure.SourceSatisfiedEvidencePaths), ", "))
		}
		if commands := featurePlannerVerificationClosureCommands(closure); len(commands) > 0 {
			fmt.Fprintf(b, "  - Commands: %s\n", strings.Join(commands, "; "))
		}
		if checks := featurePlannerVerificationClosureChecks(closure); len(checks) > 0 {
			fmt.Fprintf(b, "  - Checks: %s\n", strings.Join(checks, "; "))
		}
	}
	b.WriteString("\n")
}

func featurePlannerVerificationClosuresForPlan(plan featureplanner.FeaturePlan, closures []featureplanner.VerificationClosure) []featureplanner.VerificationClosure {
	artifactID := strings.TrimSpace(plan.Artifact.ArtifactID)
	digest := strings.TrimSpace(plan.Artifact.Digest)
	if artifactID == "" || digest == "" {
		return nil
	}
	var out []featureplanner.VerificationClosure
	for _, closure := range closures {
		if strings.TrimSpace(closure.PlanArtifactID) == artifactID && strings.TrimSpace(closure.PlanDigest) == digest {
			out = append(out, closure)
		}
	}
	return out
}

func featurePlannerReleaseReadinessForPlan(plan featureplanner.FeaturePlan, decision featureplanner.ReleaseReadinessDecision) (featureplanner.ReleaseReadinessDecision, bool) {
	artifactID := strings.TrimSpace(plan.Artifact.ArtifactID)
	digest := strings.TrimSpace(plan.Artifact.Digest)
	if artifactID == "" || digest == "" || strings.TrimSpace(string(decision.Status)) == "" {
		return featureplanner.ReleaseReadinessDecision{}, false
	}
	if strings.TrimSpace(decision.PlanArtifactID) != artifactID || strings.TrimSpace(decision.PlanDigest) != digest {
		return featureplanner.ReleaseReadinessDecision{}, false
	}
	return decision, true
}

func writeFeaturePlannerPlanTabReleaseReadiness(b *strings.Builder, decision featureplanner.ReleaseReadinessDecision) {
	if strings.TrimSpace(string(decision.Status)) == "" {
		return
	}

	b.WriteString("## Release Readiness\n\n")
	fmt.Fprintf(b, "- Status: %s\n", featurePlannerInlineMarkdown(string(decision.Status)))
	if decision.EventID != "" {
		fmt.Fprintf(b, "- Decision: %s\n", featurePlannerInlineMarkdown(decision.EventID))
	}
	if decision.PlanRevision > 0 {
		fmt.Fprintf(b, "- Plan revision: %d\n", decision.PlanRevision)
	}
	if decision.PlanDigest != "" {
		fmt.Fprintf(b, "- Digest: %s\n", featurePlannerInlineMarkdown(decision.PlanDigest))
	}
	if decision.ApprovalEventID != "" {
		fmt.Fprintf(b, "- Approval: %s\n", featurePlannerInlineMarkdown(decision.ApprovalEventID))
	}
	if len(decision.VerificationClosureEventIDs) > 0 {
		fmt.Fprintf(b, "- Verification closures: %s\n", strings.Join(featurePlannerUniqueStrings(decision.VerificationClosureEventIDs), ", "))
	}
	if len(decision.OpenObligations) > 0 {
		fmt.Fprintf(b, "- Open obligations: %s\n", strings.Join(featurePlannerUniqueStrings(decision.OpenObligations), "; "))
	}
	if len(decision.Blockers) > 0 {
		fmt.Fprintf(b, "- Blockers: %s\n", strings.Join(featurePlannerUniqueStrings(decision.Blockers), "; "))
	}
	b.WriteString("\n")
}

func featurePlannerVerificationClosureCommands(closure featureplanner.VerificationClosure) []string {
	var commands []string
	for _, result := range append(append([]featureplanner.VerificationCommandResult(nil), closure.RequiredTestResults...), closure.TDDCommandResults...) {
		command := strings.TrimSpace(result.Command)
		if command == "" {
			continue
		}
		status := strings.TrimSpace(result.Status)
		if status != "" {
			command = command + " [" + status + "]"
		}
		commands = append(commands, command)
	}

	return featurePlannerUniqueStrings(commands)
}

func featurePlannerVerificationClosureChecks(closure featureplanner.VerificationClosure) []string {
	var checks []string
	for _, check := range append(append([]featureplanner.VerificationCheckResult(nil), closure.SourceSanity...), closure.Verification...) {
		label := strings.TrimSpace(check.Kind)
		if path := strings.TrimSpace(check.Path); path != "" {
			label = firstNonEmptyFeaturePlannerPlanText(label, "check") + " " + path
		}
		if command := strings.TrimSpace(check.Command); command != "" {
			label = firstNonEmptyFeaturePlannerPlanText(label, "check") + " `" + command + "`"
		}
		if status := strings.TrimSpace(check.Status); status != "" {
			label = firstNonEmptyFeaturePlannerPlanText(label, "check") + " [" + status + "]"
		}
		if label != "" {
			checks = append(checks, label)
		}
	}

	return featurePlannerUniqueStrings(checks)
}

func writeFeaturePlannerMarkdownField(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}

	fmt.Fprintf(b, "%s: %s\n", label, featurePlannerInlineMarkdown(value))
}

func writeFeaturePlannerMarkdownCoverageSections(b *strings.Builder, state featureplanner.FeatureState, statePath string, plan featureplanner.FeaturePlan, candidate featureplanner.RejectedPlanCandidate) {
	sections := featureplanner.FeaturePlanCoveragePresentation(state, statePath, plan, candidate)
	for _, section := range sections {
		rows := make([]string, 0, len(section.Rows))
		for _, row := range section.Rows {
			rows = append(rows, row.Text)
		}
		switch section.Label {
		case featureplanner.CoverageSectionE2EJourneys,
			featureplanner.CoverageSectionFuzzAndPropertyCoverage,
			featureplanner.CoverageSectionSecurityPrivacyRAGEvals,
			featureplanner.CoverageSectionProviderContracts,
			featureplanner.CoverageSectionObservability,
			featureplanner.CoverageSectionPerformanceBounds:
			writeFeaturePlannerMarkdownCoverageListIfAny(b, section.Label, rows)
		default:
			writeFeaturePlannerMarkdownCoverageList(b, section.Label, rows)
		}
	}
}

func writeFeaturePlannerMarkdownCoverageList(b *strings.Builder, title string, values []string) {
	values = featurePlannerUniqueStrings(values)
	fmt.Fprintf(b, "## %s\n\n", title)
	if len(values) == 0 {
		b.WriteString("- No entries recorded in the structured plan.\n\n")
		return
	}

	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
	b.WriteString("\n")
}

func writeFeaturePlannerMarkdownCoverageListIfAny(b *strings.Builder, title string, values []string) {
	values = featurePlannerUniqueStrings(values)
	if len(values) == 0 {
		return
	}

	writeFeaturePlannerMarkdownCoverageList(b, title, values)
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
		b.WriteString("\n")
	}
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
