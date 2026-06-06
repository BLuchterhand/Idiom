package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"path/filepath"
)

type ParsedPlannerOutput struct {
	Kind     string
	Plan     FeaturePlan
	Question PlannerQuestion
	Raw      string
}

func ParsePlannerOutput(raw string) (ParsedPlannerOutput, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ParsedPlannerOutput{}, errors.New("planner output is empty")
	}
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return ParsedPlannerOutput{
			Kind: ToolAskPlannerQuestion,
			Question: PlannerQuestion{
				QuestionSummary: strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:")),
				Status:          "open",
			},
			Raw: trimmed,
		}, nil
	}
	var envelope struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return ParsedPlannerOutput{}, fmt.Errorf("planner output must be JSON or <<QUESTION>>: %w", err)
	}
	switch strings.TrimSpace(envelope.Tool) {
	case ToolSubmitFeaturePlan:
		var plan FeaturePlan
		if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode submit_feature_plan: %w", err)
		}
		return ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan, Raw: trimmed}, nil
	case ToolAskPlannerQuestion:
		var question PlannerQuestion
		if err := json.Unmarshal([]byte(trimmed), &question); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode ask_planner_question: %w", err)
		}
		question.Status = "open"
		return ParsedPlannerOutput{Kind: ToolAskPlannerQuestion, Question: question, Raw: trimmed}, nil
	default:
		return ParsedPlannerOutput{}, fmt.Errorf("unsupported feature planner tool %q", envelope.Tool)
	}
}

func ValidatePlan(plan FeaturePlan) error {
	var errs []error
	require(&errs, plan.Summary, "feature plan missing summary")
	require(&errs, plan.TopLevelScope.Objective, "feature plan missing top_level_scope.objective")
	if len(plan.TopLevelScope.SuccessCriteria) == 0 {
		errs = append(errs, errors.New("feature plan missing top_level_scope.success_criteria"))
	}
	if plan.CoverageGateResult != nil && plan.CoverageGateResult.Status != "pass" {
		errs = append(errs, errors.New("feature plan blocked: upstream coverage gate has not passed"))
	}
	if len(plan.SDDContract.Refs) == 0 && len(plan.SDDContract.Acceptance) == 0 {
		errs = append(errs, errors.New("feature plan missing SDD refs or acceptance criteria"))
	}
	require(&errs, plan.TDDContract.CoverageTier, "feature plan missing tdd_contract.coverage_tier")
	if !plan.TDDContract.RequiredBeforeImplementation {
		errs = append(errs, errors.New("feature plan tdd_contract.required_before_implementation must be true"))
	}
	if len(plan.TDDContract.Obligations) == 0 {
		errs = append(errs, errors.New("feature plan missing tdd_contract.obligations"))
	}
	obligations := map[string]TDDObligation{}
	for i, obligation := range plan.TDDContract.Obligations {
		prefix := fmt.Sprintf("tdd obligation %d", i)
		require(&errs, obligation.ID, prefix+" missing id")
		require(&errs, obligation.Behavior, prefix+" missing behavior")
		require(&errs, obligation.Command, prefix+" missing command")
		require(&errs, obligation.RequiredBefore, prefix+" missing required_before")
		if strings.TrimSpace(obligation.RequiredBefore) != "" && strings.TrimSpace(obligation.RequiredBefore) != "implementation" {
			errs = append(errs, fmt.Errorf("%s required_before must be implementation", prefix))
		}
		if id := strings.TrimSpace(obligation.ID); id != "" {
			obligations[id] = obligation
		}
	}
	if len(plan.Atoms) == 0 {
		errs = append(errs, errors.New("feature plan missing atoms"))
	}
	seenAtoms := map[string]bool{}
	for i, atom := range plan.Atoms {
		prefix := fmt.Sprintf("atom %d", i)
		require(&errs, atom.ID, prefix+" missing id")
		require(&errs, atom.Title, prefix+" missing title")
		require(&errs, atom.Summary, prefix+" missing summary")
		if id := strings.TrimSpace(atom.ID); id != "" {
			if seenAtoms[id] {
				errs = append(errs, fmt.Errorf("%s duplicate id %q", prefix, id))
			}
			seenAtoms[id] = true
		}
		if len(atom.RequiredEditPaths) == 0 && len(atom.AllowedEditPaths) == 0 && !atom.ImplementationRequired {
			errs = append(errs, fmt.Errorf("%s missing edit scope or source-satisfied marker", prefix))
		}
		if len(atom.SDDRefs) == 0 && len(plan.SDDContract.Refs) > 0 {
			errs = append(errs, fmt.Errorf("%s missing sdd_refs", prefix))
		}
		if !atom.TestFirst.BaselineRequired {
			errs = append(errs, fmt.Errorf("%s test_first.baseline_required must be true", prefix))
		}
		if len(atom.TestFirst.Commands) == 0 {
			errs = append(errs, fmt.Errorf("%s missing test_first.commands", prefix))
		}
		if len(atom.TestFirst.ObligationRefs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing test_first.obligation_refs", prefix))
		}
		for _, ref := range atom.TestFirst.ObligationRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, ok := obligations[ref]; !ok {
				errs = append(errs, fmt.Errorf("%s references unknown TDD obligation %q", prefix, ref))
			}
		}
	}
	if err := validateAtomDependencies(plan.Atoms); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func ValidateDurablePlan(plan FeaturePlan) error {
	var errs []error
	if plan.SchemaVersion != "" && plan.SchemaVersion != FeaturePlanSchemaVersion && plan.SchemaVersion != "idiom.feature_plan.v1" {
		errs = append(errs, fmt.Errorf("unsupported feature plan schema %q", plan.SchemaVersion))
	}
	if err := ValidatePlan(plan); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(plan.FeatureID) == "" {
		errs = append(errs, errors.New("feature plan missing feature_id"))
	}
	if strings.TrimSpace(plan.SourceTask.Kind) == "" && strings.TrimSpace(plan.SourceTask.Summary) == "" {
		errs = append(errs, errors.New("feature plan missing source_task"))
	}
	for i, atom := range plan.Atoms {
		prefix := fmt.Sprintf("atom %d", i)
		if strings.TrimSpace(atom.SchemaVersion) != "" && atom.SchemaVersion != FeatureAtomSchemaVersion {
			errs = append(errs, fmt.Errorf("%s unsupported schema %q", prefix, atom.SchemaVersion))
		}
		if atom.FeatureID != "" && plan.FeatureID != "" && atom.FeatureID != plan.FeatureID {
			errs = append(errs, fmt.Errorf("%s feature_id %q does not match plan feature_id %q", prefix, atom.FeatureID, plan.FeatureID))
		}
		if missing := MissingStrictPreDispatchTDD(atom); len(missing) > 0 {
			errs = append(errs, fmt.Errorf("%s missing durable pre-dispatch TDD fields: %s", prefix, strings.Join(missing, ", ")))
		}
		if mismatches := MismatchedStrictPreDispatchTDD(atom); len(mismatches) > 0 {
			errs = append(errs, fmt.Errorf("%s mismatched durable pre-dispatch TDD fields: %s", prefix, strings.Join(mismatches, ", ")))
		}
	}
	return errors.Join(errs...)
}

func ValidatePlanProductWorkflowContext(plan FeaturePlan, brief ProductWorkflowBrief) error {
	var errs []error
	plan = NormalizePlanProductWorkflowContext(plan, brief)
	if !brief.CoverageGatePassed {
		errs = append(errs, errors.New("feature plan submitted while product workflow coverage gate is blocked"))
	}
	if len(brief.Blockers) > 0 {
		errs = append(errs, fmt.Errorf("product workflow brief has blockers: %s", strings.Join(brief.Blockers, "; ")))
	}
	if len(brief.CoverageBlockers) > 0 {
		errs = append(errs, fmt.Errorf("product workflow coverage blockers: %s", strings.Join(brief.CoverageBlockers, "; ")))
	}

	prdIDs := stringSetFromPRDRequirements(brief.PRDRequirements)
	adrIDs := stringSet(brief.AcceptedADRIDs)
	tddIDs := stringSetFromTDDObligations(brief.TDDObligations)
	productTDD := tddObligationsByID(brief.TDDObligations)
	repoGrounding := newRepoGrounding(brief)

	if len(prdIDs) > 0 {
		if len(plan.SDDContract.PRDRequirementRefs) == 0 {
			errs = append(errs, errors.New("feature plan missing product sdd_contract.prd_requirement_refs"))
		}
		for i, ref := range plan.SDDContract.PRDRequirementRefs {
			if ref = strings.TrimSpace(ref); ref != "" && !prdIDs[ref] {
				errs = append(errs, fmt.Errorf("sdd_contract.prd_requirement_refs[%d] references unknown PRD requirement %q", i, ref))
			}
		}
	}
	if len(adrIDs) > 0 {
		if len(plan.SDDContract.ADRRefs) == 0 {
			errs = append(errs, errors.New("feature plan missing product sdd_contract.adr_refs"))
		}
		for i, ref := range plan.SDDContract.ADRRefs {
			if ref = strings.TrimSpace(ref); ref != "" && !adrIDs[ref] {
				errs = append(errs, fmt.Errorf("sdd_contract.adr_refs[%d] references unknown ADR %q", i, ref))
			}
		}
	}
	if len(tddIDs) > 0 {
		for i, obligation := range plan.TDDContract.Obligations {
			id := strings.TrimSpace(obligation.ID)
			if id != "" && !tddIDs[id] {
				errs = append(errs, fmt.Errorf("tdd_contract.obligations[%d] references unknown product TDD obligation %q", i, id))
			}
		}
	}
	for i, atom := range plan.Atoms {
		if len(normalizedStringList(atom.RequiredEditPaths)) == 0 &&
			len(normalizedStringList(atom.AllowedEditPaths)) == 0 &&
			!atom.ImplementationRequired {
			errs = append(errs, fmt.Errorf("atom %d has no product-grounded required_edit_paths or allowed_edit_paths after context-only paths were removed", i))
		}
		errs = append(errs, repoGrounding.validateAtomEditScope(i, atom)...)
		errs = append(errs, repoGrounding.validateAtomTestArtifactScope(i, atom)...)
		if readinessErrs := validateProductTDDReadiness(i, atom, productTDD); len(readinessErrs) > 0 {
			errs = append(errs, readinessErrs...)
		}
		for j, ref := range atom.SDDRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if len(prdIDs) > 0 || len(adrIDs) > 0 {
				if !prdIDs[ref] && !adrIDs[ref] {
					errs = append(errs, fmt.Errorf("atom %d sdd_refs[%d] references unknown product artifact %q", i, j, ref))
				}
			}
		}
		for j, ref := range atom.TDDObligations {
			ref = strings.TrimSpace(ref)
			if ref != "" && len(tddIDs) > 0 && !tddIDs[ref] {
				errs = append(errs, fmt.Errorf("atom %d tdd_obligations[%d] references unknown product TDD obligation %q", i, j, ref))
			}
		}
		for j, ref := range atom.TestFirst.ObligationRefs {
			ref = strings.TrimSpace(ref)
			if ref != "" && len(tddIDs) > 0 && !tddIDs[ref] {
				errs = append(errs, fmt.Errorf("atom %d test_first.obligation_refs[%d] references unknown product TDD obligation %q", i, j, ref))
			}
		}
	}
	return errors.Join(errs...)
}

func NormalizePlanProductWorkflowContext(plan FeaturePlan, brief ProductWorkflowBrief) FeaturePlan {
	grounding := newRepoGrounding(brief)
	for i := range plan.Atoms {
		atom := &plan.Atoms[i]
		required := normalizeEditScopeList(atom.RequiredEditPaths)
		requiredSet := normalizedStringSet(required)
		for _, path := range productAtomRequiredTestArtifactPaths(*atom) {
			if requiredSet[path] || !grounding.pathGrounded(path) {
				continue
			}
			required = append(required, path)
			requiredSet[path] = true
		}
		allowed := normalizeEditScopeList(atom.AllowedEditPaths)
		keptAllowed := make([]string, 0, len(allowed))
		for _, path := range allowed {
			if requiredSet[path] || grounding.pathGrounded(path) {
				keptAllowed = append(keptAllowed, path)
			}
		}
		atom.RequiredEditPaths = required
		atom.AllowedEditPaths = normalizeEditScopeList(keptAllowed)
	}
	return plan
}

func productAtomRequiredTestArtifactPaths(atom FeatureAtomPlan) []string {
	var paths []string
	if atom.TestFirst.BaselineRequired ||
		len(atom.TestFirst.ObligationRefs) > 0 ||
		len(atom.TDDObligations) > 0 ||
		strings.EqualFold(strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult), "red-confirmed") {
		paths = append(paths, atom.TestFirst.TestArtifactPaths...)
	}
	if len(atom.PlannedTDDSnapshot.ObligationIDs) > 0 ||
		len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) > 0 {
		paths = append(paths, atom.PlannedTDDSnapshot.TestArtifactPaths...)
	}
	return normalizeEditScopeList(paths)
}

func normalizeEditScopeList(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range paths {
		path := normalizeWorkspacePath(raw)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

type productRepoGrounding struct {
	filesToModify             map[string]bool
	contextEvidencePaths      map[string]bool
	authorizedNewPackageRoots map[string]bool
	packageRoots              map[string]bool
	hasLocalization           bool
}

func newRepoGrounding(brief ProductWorkflowBrief) productRepoGrounding {
	grounding := productRepoGrounding{
		filesToModify:             normalizedStringSet(brief.FilesToModify),
		contextEvidencePaths:      normalizedStringSet(brief.ContextEvidencePaths),
		authorizedNewPackageRoots: normalizedStringSet(brief.AuthorizedNewPackageRoots),
		packageRoots:              map[string]bool{},
	}
	grounding.hasLocalization = len(grounding.filesToModify) > 0 || len(grounding.contextEvidencePaths) > 0 || len(grounding.authorizedNewPackageRoots) > 0
	for path := range grounding.filesToModify {
		grounding.addExistingPackageRoot(path)
	}
	for path := range grounding.contextEvidencePaths {
		grounding.addExistingPackageRoot(path)
	}
	return grounding
}

func (g *productRepoGrounding) addExistingPackageRoot(path string) {
	dir := normalizedPathDir(path)
	if dir == "" || dir == "." {
		return
	}
	if existingPackageDir(dir) {
		g.packageRoots[dir] = true
	}
}

func (g productRepoGrounding) validateAtomEditScope(atomIndex int, atom FeatureAtomPlan) []error {
	var errs []error
	for _, scopedPath := range append(normalizedStringList(atom.RequiredEditPaths), normalizedStringList(atom.AllowedEditPaths)...) {
		if normalizeWorkspacePath(scopedPath) == "" {
			errs = append(errs, fmt.Errorf("atom %d edit path %q is not a valid repo-relative workspace path; use ask_planner_question for planning repair", atomIndex, scopedPath))
			continue
		}
		if !g.hasLocalization {
			errs = append(errs, fmt.Errorf("atom %d edit path %q is ungrounded by product workflow localization; use ask_planner_question for planning repair instead of inventing required_edit_paths or allowed_edit_paths", atomIndex, scopedPath))
			continue
		}
		if g.pathGrounded(scopedPath) {
			continue
		}
		errs = append(errs, fmt.Errorf("atom %d edit path %q is ungrounded by files_to_modify, context/source evidence, existing grounded package roots, or authorized new package roots; use ask_planner_question for planning repair", atomIndex, scopedPath))
	}
	return errs
}

func (g productRepoGrounding) validateAtomTestArtifactScope(atomIndex int, atom FeatureAtomPlan) []error {
	var errs []error
	paths := normalizedStringList(append(atom.TestFirst.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths...))
	for _, scopedPath := range paths {
		if normalizeWorkspacePath(scopedPath) == "" {
			errs = append(errs, fmt.Errorf("atom %d test artifact path %q is not a valid repo-relative workspace path; use ask_planner_question for planning repair", atomIndex, scopedPath))
			continue
		}
		if !g.hasLocalization {
			errs = append(errs, fmt.Errorf("atom %d test artifact path %q is ungrounded by product workflow localization; use ask_planner_question for planning repair instead of inventing test artifact paths", atomIndex, scopedPath))
			continue
		}
		if g.pathGrounded(scopedPath) {
			continue
		}
		errs = append(errs, fmt.Errorf("atom %d test artifact path %q is ungrounded by files_to_modify, context/source evidence, existing grounded package roots, or authorized new package roots; use ask_planner_question for planning repair", atomIndex, scopedPath))
	}
	return errs
}

func (g productRepoGrounding) pathGrounded(path string) bool {
	path = normalizeWorkspacePath(path)
	if path == "" {
		return false
	}
	if g.filesToModify[path] || g.contextEvidencePaths[path] {
		return true
	}
	if pathWithinAnyRoot(path, g.authorizedNewPackageRoots) {
		return true
	}
	if pathWithinAnyRoot(path, g.packageRoots) {
		return true
	}
	return false
}

func validateProductTDDReadiness(atomIndex int, atom FeatureAtomPlan, productTDD map[string]TDDObligationBrief) []error {
	var errs []error
	refs := normalizedStringList(append(atom.TDDObligations, atom.TestFirst.ObligationRefs...))
	for _, ref := range refs {
		obligation, ok := productTDD[ref]
		if !ok || !requiredBeforeImplementation(obligation.RequiredBefore) {
			continue
		}
		if !looksExecutableCommand(obligation.Command) {
			errs = append(errs, fmt.Errorf("atom %d product TDD obligation %q is required before implementation but lacks an executable command", atomIndex, ref))
		}
		if len(atom.TestFirst.TestArtifactPaths) == 0 {
			errs = append(errs, fmt.Errorf("atom %d product TDD obligation %q is required before implementation but atom test_first lacks a test artifact path", atomIndex, ref))
		}
	}
	for _, command := range atom.TestFirst.Commands {
		if !looksExecutableCommand(command) {
			errs = append(errs, fmt.Errorf("atom %d test_first command %q is not an executable command artifact", atomIndex, command))
		}
	}
	return errs
}

func requiredBeforeImplementation(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "" || value == "implementation"
}

func looksExecutableCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	lower := strings.ToLower(command)
	generic := []string{"run tests", "run the tests", "test", "tests", "tbd", "planned", "todo", "n/a", "none"}
	for _, value := range generic {
		if lower == value {
			return false
		}
	}
	for _, prefix := range []string{"go ", "make ", "npm ", "yarn ", "pnpm ", "node ", "python ", "python3 ", "pytest", "cargo ", "mvn ", "gradle ", "./", "sh ", "bash "} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return strings.Contains(command, " ")
}

func tddObligationsByID(obligations []TDDObligationBrief) map[string]TDDObligationBrief {
	out := map[string]TDDObligationBrief{}
	for _, obligation := range obligations {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			out[id] = obligation
		}
	}
	return out
}

func existingPackageDir(dir string) bool {
	entries, err := os.ReadDir(repoRelativePath(dir))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") || name == "package.json" || name == "pyproject.toml" || name == "Cargo.toml" || name == "go.mod" {
			return true
		}
	}
	return false
}

func repoRelativePath(path string) string {
	path = filepath.FromSlash(path)
	dir, err := os.Getwd()
	if err != nil {
		return path
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir = parent
	}
}

func normalizedPathDir(path string) string {
	path = normalizeWorkspacePath(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
}

func normalizeWorkspacePath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" || filepath.IsAbs(path) {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return ""
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

func pathWithinAnyRoot(path string, roots map[string]bool) bool {
	path = normalizeWorkspacePath(path)
	if path == "" {
		return false
	}
	for root := range roots {
		root = strings.TrimRight(normalizeWorkspacePath(root), "/")
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func stringSetFromPRDRequirements(requirements []PRDRequirementBrief) map[string]bool {
	out := map[string]bool{}
	for _, requirement := range requirements {
		if id := strings.TrimSpace(requirement.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func stringSetFromTDDObligations(obligations []TDDObligationBrief) map[string]bool {
	out := map[string]bool{}
	for _, obligation := range obligations {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func ValidateDurableState(state FeatureState) error {
	var errs []error
	if state.SchemaVersion != "" && state.SchemaVersion != SchemaVersion && state.SchemaVersion != FeatureStateSchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported feature state schema %q", state.SchemaVersion))
	}
	if strings.TrimSpace(state.FeatureID) == "" && strings.TrimSpace(state.SessionID) == "" {
		errs = append(errs, errors.New("feature state missing feature_id or session_id"))
	}
	if strings.TrimSpace(state.Status) == "" {
		errs = append(errs, errors.New("feature state missing status"))
	} else if !isValidFeatureState(state.Status) {
		errs = append(errs, fmt.Errorf("feature state has unknown status %q", state.Status))
	}
	if state.Revision < 0 {
		errs = append(errs, errors.New("feature state revision must be non-negative"))
	}
	if isTerminalFeatureState(state.Status) {
		if len(state.ActiveRuns) > 0 {
			errs = append(errs, errors.New("terminal feature state has active runs"))
		}
		if hasOpenQuestion(state.OpenQuestions) || len(state.PlanningLoop.OpenQuestionIDs) > 0 {
			errs = append(errs, errors.New("terminal feature state has open planner questions"))
		}
		if len(state.OpenRecoveryPackets) > 0 {
			errs = append(errs, errors.New("terminal feature state has open recovery packets"))
		}
	}
	for i, atom := range state.Atoms {
		if strings.TrimSpace(atom.State) == "" {
			errs = append(errs, fmt.Errorf("atom %d missing state", i))
		} else if !isValidAtomState(atom.State) {
			errs = append(errs, fmt.Errorf("atom %d has unknown state %q", i, atom.State))
		}
	}
	for i, question := range state.OpenQuestions {
		if err := ValidatePlannerQuestion(question); err != nil {
			errs = append(errs, fmt.Errorf("planner question %d invalid: %w", i, err))
		}
	}
	for i, run := range state.ActiveRuns {
		if strings.TrimSpace(run.RunID) == "" && strings.TrimSpace(run.LaneSessionID) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing run identity", i))
		}
		if strings.TrimSpace(run.AtomID) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing atom_id", i))
		}
		if strings.TrimSpace(run.State) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing state", i))
		}
	}
	for i, evidence := range state.TDDPreExecutionEvidence {
		if strings.TrimSpace(evidence.EventID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing event_id", i))
		}
		if strings.TrimSpace(evidence.AtomID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing atom_id", i))
		}
		if strings.TrimSpace(evidence.SnapshotID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing snapshot_id", i))
		}
		if strings.TrimSpace(evidence.SnapshotDigest) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing snapshot_digest", i))
		}
	}
	return errors.Join(errs...)
}

func ApplyPlannerOutput(state *FeatureState, parsed ParsedPlannerOutput, now time.Time) []Event {
	if state == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.PlanningLoop.Iteration++
	switch parsed.Kind {
	case ToolAskPlannerQuestion:
		question := normalizePlannerQuestion(parsed.Question, fmt.Sprintf("q-%03d", len(state.OpenQuestions)+1), "", now)
		if err := ValidatePlannerQuestion(question); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, Message: err.Error()}}
		}
		state.OpenQuestions = append(state.OpenQuestions, question)
		state.Status = FeatureStatePlanningQuestion
		state.PlanningLoop.State = FeatureStatePlanningQuestion
		return []Event{{Type: "idiom.feature_planner.planner_question_opened", SessionID: state.SessionID, Message: question.QuestionSummary}}
	case ToolSubmitFeaturePlan:
		hydrateDurablePlanDefaults(&parsed.Plan, state, now)
		if err := ValidateDurablePlan(parsed.Plan); err != nil {
			state.Plan = parsed.Plan
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return []Event{{Type: "idiom.feature_planner.tdd_pre_execution_blocked", SessionID: state.SessionID, Message: err.Error()}}
		}
		state.Plan = parsed.Plan
		state.Atoms = make([]FeatureAtomState, 0, len(parsed.Plan.Atoms))
		for _, atom := range parsed.Plan.Atoms {
			state.Atoms = append(state.Atoms, FeatureAtomState{
				FeatureAtomPlan: atom,
				State:           AtomStateQueued,
			})
		}
		state.Status = FeatureStateDispatching
		state.PlanningLoop.State = FeatureStateDispatching
		if len(state.Atoms) > 0 {
			state.Continuation.NextAtomID = state.Atoms[0].ID
			state.Continuation.ResumeAction = "dispatch_next_atom"
		}
		events := []Event{
			{Type: "idiom.feature_planner.plan_accepted", SessionID: state.SessionID, FeatureTo: FeatureStatePlanReview, Fields: map[string]any{"atom_count": len(state.Atoms)}},
			{Type: "idiom.feature_planner.plan_reviewed", SessionID: state.SessionID, FeatureFrom: FeatureStatePlanReview, FeatureTo: FeatureStateAwaitingApproval, Fields: map[string]any{"review": "durable_plan_contract_passed"}},
			{Type: "idiom.feature_planner.plan_approved", SessionID: state.SessionID, FeatureFrom: FeatureStateAwaitingApproval, FeatureTo: FeatureStateAtomizing, Fields: map[string]any{"approval": "harness_contract_approval"}},
			{Type: "idiom.feature_planner.atoms_compiled", SessionID: state.SessionID, FeatureFrom: FeatureStateAtomizing, FeatureTo: FeatureStateReady, Fields: map[string]any{"atom_count": len(state.Atoms)}},
		}
		events = append(events, recordTDDPreExecutionEvidence(state, now)...)
		events = append(events, Event{Type: "idiom.feature_planner.dispatch_ready", SessionID: state.SessionID, FeatureFrom: FeatureStateReady, FeatureTo: FeatureStateDispatching})
		return events
	default:
		state.Status = FeatureStateMalformedOutput
		state.PlanningLoop.State = FeatureStateMalformedOutput
		state.LastError = "unsupported parsed planner output"
		return []Event{{Type: "idiom.feature_planner.malformed_structured_output", SessionID: state.SessionID, Message: state.LastError}}
	}
}

func recordTDDPreExecutionEvidence(state *FeatureState, now time.Time) []Event {
	if state == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdAt := now.UTC().Format(time.RFC3339Nano)
	var events []Event
	for _, atom := range state.Atoms {
		evidence := tddEvidenceFromAtom(atom.FeatureAtomPlan, createdAt)
		if evidence.EventID == "" {
			continue
		}
		upsertTDDEvidence(state, evidence)
		events = append(events, Event{
			Type:      "idiom.feature_planner.tdd_pre_execution_checked",
			SessionID: state.SessionID,
			AtomID:    atom.ID,
			EventID:   evidence.EventID,
			Actor:     "harness",
			Fields: map[string]any{
				"obligation_refs":               evidence.ObligationRefs,
				"commands":                      evidence.Commands,
				"test_artifact_paths":           evidence.TestArtifactPaths,
				"expected_pre_execution_result": evidence.ExpectedPreExecutionResult,
				"coverage_gate_event_id":        evidence.CoverageGateEventID,
				"snapshot_id":                   evidence.SnapshotID,
				"snapshot_digest":               evidence.SnapshotDigest,
			},
		})
	}
	return events
}

func tddEvidenceFromAtom(atom FeatureAtomPlan, createdAt string) TDDPreExecutionEvidence {
	return TDDPreExecutionEvidence{
		EventID:                    strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID),
		AtomID:                     strings.TrimSpace(atom.ID),
		ObligationRefs:             normalizedStringList(atom.TestFirst.ObligationRefs),
		Commands:                   normalizedStringList(atom.TestFirst.Commands),
		TestArtifactPaths:          normalizedStringList(atom.TestFirst.TestArtifactPaths),
		ExpectedPreExecutionResult: strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult),
		CoverageGateEventID:        strings.TrimSpace(atom.TestFirst.CoverageGateEventID),
		SnapshotID:                 strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID),
		SnapshotDigest:             strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest),
		CreatedAt:                  createdAt,
	}
}

func upsertTDDEvidence(state *FeatureState, evidence TDDPreExecutionEvidence) {
	if state == nil || evidence.EventID == "" || evidence.AtomID == "" {
		return
	}
	for i := range state.TDDPreExecutionEvidence {
		existing := state.TDDPreExecutionEvidence[i]
		if existing.EventID == evidence.EventID && existing.AtomID == evidence.AtomID {
			state.TDDPreExecutionEvidence[i] = evidence
			return
		}
	}
	state.TDDPreExecutionEvidence = append(state.TDDPreExecutionEvidence, evidence)
}

func MarkPlanningStuck(state *FeatureState, reason string) {
	if state == nil {
		return
	}
	state.Status = FeatureStatePlanningStuck
	state.PlanningLoop.State = FeatureStatePlanningStuck
	state.PlanningLoop.StuckReason = strings.TrimSpace(reason)
	if state.PlanningLoop.StuckReason == "" {
		state.PlanningLoop.StuckReason = "planning loop exceeded maximum iterations"
	}
}

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}

func MissingStrictPreDispatchTDD(atom FeatureAtomPlan) []string {
	missing := MissingPreDispatchPlanTDD(atom)
	if len(atom.TestFirst.TestArtifactPaths) == 0 {
		missing = append(missing, "test_first.test_artifact_paths")
	}
	if len(atom.TestFirst.PreExecutionEventIDs) == 0 {
		missing = append(missing, "test_first.pre_execution_event_ids")
	}
	if strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult) == "" {
		missing = append(missing, "test_first.expected_pre_execution_result")
	}
	if strings.TrimSpace(atom.TestFirst.CoverageGateEventID) == "" {
		missing = append(missing, "test_first.coverage_gate_event_id")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID) == "" {
		missing = append(missing, "planned_tdd_snapshot.snapshot_id")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest) == "" {
		missing = append(missing, "planned_tdd_snapshot.snapshot_digest")
	}
	if len(atom.PlannedTDDSnapshot.ObligationIDs) == 0 {
		missing = append(missing, "planned_tdd_snapshot.obligation_ids")
	}
	if len(atom.PlannedTDDSnapshot.TestCommands) == 0 {
		missing = append(missing, "planned_tdd_snapshot.test_commands")
	}
	if len(atom.PlannedTDDSnapshot.TestArtifactPaths) == 0 {
		missing = append(missing, "planned_tdd_snapshot.test_artifact_paths")
	}
	if len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) == 0 {
		missing = append(missing, "planned_tdd_snapshot.expected_pre_execution_results")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) == "" {
		missing = append(missing, "planned_tdd_snapshot.created_before_dispatch_event_id")
	}
	return missing
}

func MismatchedStrictPreDispatchTDD(atom FeatureAtomPlan) []string {
	var mismatches []string
	if len(atom.TestFirst.ObligationRefs) > 0 && len(atom.PlannedTDDSnapshot.ObligationIDs) > 0 &&
		!sameStringSet(atom.TestFirst.ObligationRefs, atom.PlannedTDDSnapshot.ObligationIDs) {
		mismatches = append(mismatches, "planned_tdd_snapshot.obligation_ids")
	}
	if len(atom.TestFirst.Commands) > 0 && len(atom.PlannedTDDSnapshot.TestCommands) > 0 &&
		!sameStringSet(atom.TestFirst.Commands, atom.PlannedTDDSnapshot.TestCommands) {
		mismatches = append(mismatches, "planned_tdd_snapshot.test_commands")
	}
	if len(atom.TestFirst.TestArtifactPaths) > 0 && len(atom.PlannedTDDSnapshot.TestArtifactPaths) > 0 &&
		!sameStringSet(atom.TestFirst.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths) {
		mismatches = append(mismatches, "planned_tdd_snapshot.test_artifact_paths")
	}
	if strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult) != "" &&
		len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) > 0 &&
		!containsString(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults, atom.TestFirst.ExpectedPreExecutionResult) {
		mismatches = append(mismatches, "planned_tdd_snapshot.expected_pre_execution_results")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) != "" &&
		len(atom.TestFirst.PreExecutionEventIDs) > 0 &&
		!containsString(atom.TestFirst.PreExecutionEventIDs, atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) {
		mismatches = append(mismatches, "planned_tdd_snapshot.created_before_dispatch_event_id")
	}
	return mismatches
}

func ValidatePlannerQuestion(question PlannerQuestion) error {
	var errs []error
	require(&errs, question.ID, "planner question missing id")
	require(&errs, question.QuestionSummary, "planner question missing question_summary")
	require(&errs, question.QuestionKind, "planner question missing question_kind")
	require(&errs, question.BlockingClaim, "planner question missing blocking_claim")
	require(&errs, question.RequestedDecision, "planner question missing requested_decision")
	require(&errs, question.AuthorityRequired, "planner question missing authority_required")
	require(&errs, question.RequiredBefore, "planner question missing required_before")
	if question.QuestionKind != "" && !isValidQuestionKind(question.QuestionKind) {
		errs = append(errs, fmt.Errorf("planner question has unknown question_kind %q", question.QuestionKind))
	}
	if question.AuthorityRequired != "" && !isValidQuestionAuthority(question.AuthorityRequired) {
		errs = append(errs, fmt.Errorf("planner question has unknown authority_required %q", question.AuthorityRequired))
	}
	if question.Status != "" && question.Status != "open" && question.Status != "answered" && question.Status != "rejected" {
		errs = append(errs, fmt.Errorf("planner question has unknown status %q", question.Status))
	}
	return errors.Join(errs...)
}

func MissingPreDispatchPlanTDD(atom FeatureAtomPlan) []string {
	var missing []string
	if !atom.TestFirst.BaselineRequired {
		missing = append(missing, "test_first.baseline_required")
	}
	if len(atom.TestFirst.Commands) == 0 {
		missing = append(missing, "test_first.commands")
	}
	if len(atom.TestFirst.ObligationRefs) == 0 {
		missing = append(missing, "test_first.obligation_refs")
	}
	return missing
}

func isTerminalFeatureState(status string) bool {
	switch status {
	case FeatureStateReleased, FeatureStateClosedWithRisk, FeatureStateCanceled, FeatureStateComplete:
		return true
	default:
		return false
	}
}

func isValidFeatureState(status string) bool {
	switch status {
	case FeatureStateDraftingPlan, FeatureStatePlanning, FeatureStatePlanningRepair, FeatureStatePlanningQuestion,
		FeatureStatePlanningStuck, FeatureStatePlanReview, FeatureStateAwaitingApproval, FeatureStateAtomizing,
		FeatureStateReplanning, FeatureStateReady, FeatureStateDispatching, FeatureStateVerifying,
		FeatureStateReviewing, FeatureStateReconciling, FeatureStateNeedsContextRefresh,
		FeatureStateRecoveryOpen, FeatureStateBlocked, FeatureStateReleaseReview, FeatureStateReleased,
		FeatureStateClosedWithRisk, FeatureStateCanceled, FeatureStateInterrupted, FeatureStateComplete,
		FeatureStateFailed, FeatureStateSourceSatisfied, FeatureStateMalformedOutput, FeatureStateStaleEditAnchor:
		return true
	default:
		return false
	}
}

func isValidAtomState(status string) bool {
	switch status {
	case AtomStatePlanned, AtomStateQueued, AtomStateDispatchable, AtomStateDispatching, AtomStateImplementing,
		AtomStateQuestionForPlanner, AtomStateQuestionForOperator, AtomStateSourceSatisfied,
		AtomStateMalformedOutput, AtomStateStaleEditAnchor, AtomStateVerifying, AtomStateReviewing,
		AtomStateNeedsContextRefresh, AtomStateComplete, AtomStateStaleNeedsRebase,
		AtomStateBlockedDirtyFiles, AtomStateMissingScope, AtomStateInvalidScope, AtomStateFailedRefineDraft,
		AtomStateRecoveryOpen, AtomStateBlocked, AtomStateSkipped, AtomStateFailed, AtomStateCanceled,
		AtomStateInterrupted, AtomStateBlockedMissingTDD:
		return true
	default:
		return false
	}
}

func isValidQuestionKind(kind string) bool {
	switch kind {
	case "missing_spec", "conflicting_spec", "scope_split_needed", "test_gap", "stale_context", "unsafe_scope", "external_answer_required":
		return true
	default:
		return false
	}
}

func isValidQuestionAuthority(authority string) bool {
	switch authority {
	case "accepted_source", "operator_approval", "harness_policy", "fresh_context", "atom_local_evidence":
		return true
	default:
		return false
	}
}

func hasOpenQuestion(questions []PlannerQuestion) bool {
	for _, question := range questions {
		if question.Status == "open" {
			return true
		}
	}
	return false
}

func sameStringSet(left []string, right []string) bool {
	leftSet := normalizedStringSet(left)
	rightSet := normalizedStringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if !rightSet[value] {
			return false
		}
	}
	return true
}

func normalizedStringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func normalizedStringList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsString(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func normalizePlannerQuestion(question PlannerQuestion, fallbackID string, atomID string, now time.Time) PlannerQuestion {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(question.ID) == "" {
		question.ID = strings.TrimSpace(question.QuestionID)
	}
	if strings.TrimSpace(question.ID) == "" {
		question.ID = fallbackID
	}
	if strings.TrimSpace(question.QuestionID) == "" {
		question.QuestionID = question.ID
	}
	if strings.TrimSpace(question.AtomID) == "" {
		question.AtomID = atomID
	}
	if strings.TrimSpace(question.QuestionKind) == "" {
		question.QuestionKind = "external_answer_required"
	}
	if strings.TrimSpace(question.BlockingClaim) == "" {
		question.BlockingClaim = question.QuestionSummary
	}
	if strings.TrimSpace(question.RequestedDecision) == "" {
		question.RequestedDecision = question.QuestionSummary
	}
	if strings.TrimSpace(question.AuthorityRequired) == "" {
		question.AuthorityRequired = "operator_approval"
	}
	if strings.TrimSpace(question.RequiredBefore) == "" {
		question.RequiredBefore = "plan_approval"
		if atomID != "" {
			question.RequiredBefore = "atom_dispatch"
		}
	}
	if strings.TrimSpace(question.Status) == "" {
		question.Status = "open"
	}
	if strings.TrimSpace(question.AskedAt) == "" {
		question.AskedAt = now.UTC().Format(time.RFC3339Nano)
	}
	return question
}

func hydrateDurablePlanDefaults(plan *FeaturePlan, state *FeatureState, now time.Time) {
	if plan == nil || state == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(plan.SchemaVersion) == "" {
		plan.SchemaVersion = FeaturePlanSchemaVersion
	}
	if strings.TrimSpace(plan.FeatureID) == "" {
		plan.FeatureID = firstNonEmpty(state.FeatureID, state.SessionID)
	}
	if strings.TrimSpace(plan.SourceTask.Kind) == "" {
		plan.SourceTask.Kind = "operator_prompt"
	}
	if strings.TrimSpace(plan.SourceTask.Summary) == "" {
		plan.SourceTask.Summary = state.PromptSHA256
	}
	for i := range plan.Atoms {
		atom := &plan.Atoms[i]
		if strings.TrimSpace(atom.SchemaVersion) == "" {
			atom.SchemaVersion = FeatureAtomSchemaVersion
		}
		if strings.TrimSpace(atom.FeatureID) == "" {
			atom.FeatureID = plan.FeatureID
		}
		if strings.TrimSpace(atom.AtomID) == "" {
			atom.AtomID = atom.ID
		}
		if strings.TrimSpace(atom.TestFirst.CoverageGateEventID) == "" && len(atom.TestFirst.PreExecutionEventIDs) > 0 {
			atom.TestFirst.CoverageGateEventID = atom.TestFirst.PreExecutionEventIDs[0]
		}
		if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) == "" && len(atom.TestFirst.PreExecutionEventIDs) > 0 {
			atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = atom.TestFirst.PreExecutionEventIDs[0]
		}
		_ = now
	}
}

func validateAtomDependencies(atoms []FeatureAtomPlan) error {
	ids := map[string]bool{}
	depsByAtom := map[string][]string{}
	var errs []error
	for i, atom := range atoms {
		id := strings.TrimSpace(atom.ID)
		if id == "" {
			continue
		}
		ids[id] = true
		for _, dep := range atom.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				errs = append(errs, fmt.Errorf("atom %d has empty dependency", i))
				continue
			}
			if dep == id {
				errs = append(errs, fmt.Errorf("atom %d depends on itself %q", i, id))
				continue
			}
			depsByAtom[id] = append(depsByAtom[id], dep)
		}
	}
	for id, deps := range depsByAtom {
		for _, dep := range deps {
			if !ids[dep] {
				errs = append(errs, fmt.Errorf("atom %q depends on unknown atom %q", id, dep))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("atom dependency cycle includes %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range depsByAtom[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
