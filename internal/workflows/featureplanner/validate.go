package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/util"
)

type ParsedPlannerOutput struct {
	Kind            string
	Plan            FeaturePlan
	Question        PlannerQuestion
	SourceSatisfied SourceSatisfiedCloseout
	Raw             string
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
	case ToolSourceSatisfied:
		var closeout SourceSatisfiedCloseout
		if err := json.Unmarshal([]byte(trimmed), &closeout); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode source_satisfied: %w", err)
		}
		closeout.Tool = ToolSourceSatisfied
		return ParsedPlannerOutput{Kind: ToolSourceSatisfied, SourceSatisfied: closeout, Raw: trimmed}, nil
	case ToolAskPlannerQuestion:
		var question PlannerQuestion
		if err := json.Unmarshal([]byte(trimmed), &question); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode ask_planner_question: %w", err)
		}
		question = plannerQuestionFromAliasFields(trimmed, question)
		question.Status = "open"
		return ParsedPlannerOutput{Kind: ToolAskPlannerQuestion, Question: question, Raw: trimmed}, nil
	default:
		return ParsedPlannerOutput{}, fmt.Errorf("unsupported feature planner tool %q", envelope.Tool)
	}
}

func ValidateSourceSatisfiedCloseout(closeout SourceSatisfiedCloseout) error {
	var errs []error
	require(&errs, closeout.Summary, "source_satisfied missing summary")
	if len(nonEmptyFeaturePlannerStrings(closeout.EvidencePaths)) == 0 {
		errs = append(errs, errors.New("source_satisfied missing evidence_paths"))
	}
	if len(nonEmptySourceSatisfiedAnchors(closeout.Anchors)) == 0 {
		errs = append(errs, errors.New("source_satisfied missing anchors"))
	}
	return errors.Join(errs...)
}

func ValidateSourceSatisfiedWorkspaceContext(closeout SourceSatisfiedCloseout, workspaceRoot string) error {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	var errs []error
	for _, path := range append(closeout.EvidencePaths, closeout.TestPaths...) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) {
			errs = append(errs, fmt.Errorf("source_satisfied evidence path %q must be repo-relative", path))
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(path))
		full := filepath.Join(workspaceRoot, clean)
		if rel, err := filepath.Rel(workspaceRoot, full); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			errs = append(errs, fmt.Errorf("source_satisfied evidence path %q escapes workspace", path))
			continue
		}
		if _, err := os.Stat(full); err != nil {
			if os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("source_satisfied evidence path %q does not exist", path))
				continue
			}
			errs = append(errs, fmt.Errorf("source_satisfied evidence path %q unavailable: %w", path, err))
		}
	}
	for _, anchor := range nonEmptySourceSatisfiedAnchors(closeout.Anchors) {
		path := strings.TrimSpace(anchor.Path)
		text := strings.TrimSpace(anchor.Text)
		if filepath.IsAbs(path) {
			errs = append(errs, fmt.Errorf("source_satisfied anchor path %q must be repo-relative", path))
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(path))
		full := filepath.Join(workspaceRoot, clean)
		if rel, err := filepath.Rel(workspaceRoot, full); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			errs = append(errs, fmt.Errorf("source_satisfied anchor path %q escapes workspace", path))
			continue
		}
		body, err := os.ReadFile(full)
		if err != nil {
			if os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("source_satisfied anchor path %q does not exist", path))
				continue
			}
			errs = append(errs, fmt.Errorf("source_satisfied anchor path %q unavailable: %w", path, err))
			continue
		}
		if !strings.Contains(string(body), text) {
			errs = append(errs, fmt.Errorf("source_satisfied anchor text not found in %q", path))
		}
	}
	return errors.Join(errs...)
}

func plannerQuestionFromAliasFields(raw string, question PlannerQuestion) PlannerQuestion {
	if strings.TrimSpace(question.QuestionSummary) != "" {
		return question
	}
	var aliases struct {
		Question string `json:"question"`
		Summary  string `json:"summary"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &aliases); err != nil {
		return question
	}
	question.QuestionSummary = firstNonEmptyTrimmed(aliases.Question, aliases.Summary, aliases.Message)
	return question
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
	if !sddContractHasEvidence(plan.SDDContract) {
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
		if len(atom.SDDRefs) == 0 && sddContractHasEvidence(plan.SDDContract) {
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
		requiredPathSet := normalizedStringSet(atom.RequiredEditPaths)
		for _, path := range requiredTDDTestArtifactPaths(atom) {
			if !requiredPathSet[path] {
				errs = append(errs, fmt.Errorf("%s required TDD test artifact path %q must be covered by atom.required_edit_paths, not omitted or demoted to allowed_edit_paths", prefix, path))
			}
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

func sddContractHasEvidence(contract SDDContract) bool {
	return len(nonEmptyFeaturePlannerStrings(contract.Refs)) > 0 ||
		len(nonEmptyFeaturePlannerStrings(contract.Acceptance)) > 0 ||
		len(nonEmptyFeaturePlannerStrings(contract.PRDRequirementRefs)) > 0 ||
		len(nonEmptyFeaturePlannerStrings(contract.ADRRefs)) > 0
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
		for _, invalid := range InvalidStrictPreDispatchTDD(atom) {
			errs = append(errs, fmt.Errorf("%s invalid durable pre-dispatch TDD field: %s", prefix, invalid))
		}
	}
	return errors.Join(errs...)
}

func ValidatePlanProductWorkflowContext(plan FeaturePlan, brief ProductWorkflowBrief, workspaceRoot string) error {
	var errs []error
	errs = append(errs, validatePlanCoversFilesToModify(plan, brief)...)
	plan = NormalizePlanProductWorkflowContext(plan, brief, workspaceRoot)
	allowDispatch := brief.AllowsFeaturePlanDispatch()
	if !allowDispatch {
		errs = append(errs, errors.New("feature plan submitted while product workflow coverage gate is blocked"))
	}
	if len(brief.Blockers) > 0 {
		errs = append(errs, fmt.Errorf("product workflow brief has blockers: %s", strings.Join(brief.Blockers, "; ")))
	}
	if len(brief.CoverageBlockers) > 0 && !allowDispatch {
		errs = append(errs, fmt.Errorf("product workflow coverage blockers: %s", strings.Join(brief.CoverageBlockers, "; ")))
	}

	prdIDs := stringSetFromPRDRequirements(brief.PRDRequirements)
	adrIDs := stringSet(brief.AcceptedADRIDs)
	tddIDs := stringSetFromTDDObligations(brief.TDDObligations)
	productTDD := tddObligationsByID(brief.TDDObligations)
	repoGrounding := newRepoGrounding(brief, workspaceRoot)

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

type StandaloneWorkspaceContext struct {
	WorkspaceRoot string
	Contract      plancontract.PlanExecutionContract
}

func ValidatePlanStandaloneWorkspaceContext(plan FeaturePlan, context StandaloneWorkspaceContext) error {
	root := strings.TrimSpace(context.WorkspaceRoot)
	if root == "" {
		return nil
	}
	var errs []error
	for i, atom := range plan.Atoms {
		for _, scopedPath := range standaloneAtomWorkspaceScopePaths(atom) {
			path := plancontract.NormalizeScopePath(scopedPath)
			if path == "" {
				errs = append(errs, fmt.Errorf("atom %d edit path %q is not a valid repo-relative workspace path; use ask_planner_question for localization repair", i, scopedPath))
				continue
			}
			if err := validateStandaloneWorkspacePath(root, context.Contract, path); err != nil {
				errs = append(errs, fmt.Errorf("atom %d edit path %q %w", i, path, err))
			}
		}
		for _, scopedPath := range standaloneAtomTestArtifactPaths(atom) {
			path := plancontract.NormalizeScopePath(scopedPath)
			if path == "" {
				errs = append(errs, fmt.Errorf("atom %d test artifact path %q is not a valid repo-relative workspace path; use ask_planner_question for localization repair", i, scopedPath))
			}
		}
	}
	return errors.Join(errs...)
}

func standaloneAtomWorkspaceScopePaths(atom FeatureAtomPlan) []string {
	return append(append([]string(nil), atom.RequiredEditPaths...), atom.AllowedEditPaths...)
}

func standaloneAtomTestArtifactPaths(atom FeatureAtomPlan) []string {
	return append(append([]string(nil), atom.TestFirst.TestArtifactPaths...), atom.PlannedTDDSnapshot.TestArtifactPaths...)
}

func validateStandaloneWorkspacePath(root string, contract plancontract.PlanExecutionContract, path string) error {
	abs := filepath.Join(root, filepath.FromSlash(path))
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			if planAuthorityContractNewFileAuthorized(contract, path) {
				return nil
			}
			return errors.New("is missing from the workspace and is not authorized as a new file; use ask_planner_question or source-satisfied evidence with existing paths instead of inventing paths")
		}
		return fmt.Errorf("could not be statted before dispatch: %w", err)
	}
	return nil
}

func CompilePlanAuthorityContract(plan FeaturePlan, deltas []PlanAuthorityDelta) plancontract.PlanExecutionContract {
	contract := plancontract.PlanExecutionContract{
		PlanID:             plan.FeatureID,
		AuthorizedNewFiles: newFileAuthorizationsFromDeltas(deltas),
		AuthorityDeltas:    planContractAuthorityDeltas(deltas),
	}
	for _, atom := range plan.Atoms {
		atomContract := compileAtomAuthorityContract(plan, atom)
		contract.Atoms = append(contract.Atoms, atomContract)
	}
	return contract.Normalized()
}

func compileAtomAuthorityContract(plan FeaturePlan, atom FeatureAtomPlan) plancontract.AtomExecutionContract {
	required := normalizeEditScopeList(atom.RequiredEditPaths)
	required = normalizeEditScopeList(append(required, requiredTDDTestArtifactPaths(atom)...))
	allowed := featurePlannerAllowedScopeExcludingRequired(normalizeEditScopeList(atom.AllowedEditPaths), required)
	obligations := atomEditSetObligations(atom, required)
	tddByID := featurePlannerTDDObligationsByID(plan.TDDContract.Obligations)
	scope := plancontract.NewPlanScopeContract(required, allowed, obligations)
	scope = scope.WithWriteAllowedPaths(append(append([]string(nil), required...), allowed...))
	contract := plancontract.AtomExecutionContract{
		AtomID:    strings.TrimSpace(atom.ID),
		AtomTitle: strings.TrimSpace(atom.Title),
		Scope:     scope,
	}
	for _, command := range normalizedStringList(append(atom.RequiredTests, atom.TestFirst.Commands...)) {
		contract.RequiredTests = append(contract.RequiredTests, plancontract.RequiredTest{Command: command, Source: "featureplanner"})
	}
	for _, command := range normalizedStringList(atom.TestFirst.Commands) {
		tddID := firstNonEmptyStringSlice(atom.TestFirst.ObligationRefs)
		tdd := tddByID[strings.TrimSpace(tddID)]
		contract.TDDObligations = append(contract.TDDObligations, plancontract.TDDObligation{
			ID:                tddID,
			Command:           command,
			Behavior:          strings.TrimSpace(tdd.Behavior),
			TestArtifactPaths: normalizeEditScopeList(append(atom.TestFirst.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths...)),
			Source:            "featureplanner.test_first",
		})
	}
	for _, acceptance := range normalizedStringList(append(append(append([]string(nil), plan.SDDContract.Refs...), plan.SDDContract.Acceptance...), atom.SDDRefs...)) {
		contract.AcceptanceObligations = append(contract.AcceptanceObligations, plancontract.AcceptanceEvidenceRequired{Description: acceptance, Required: true, Source: "featureplanner.sdd"})
	}
	for _, path := range normalizeEditScopeList(atom.EvidencePaths) {
		contract.SourceEvidenceObligations = append(contract.SourceEvidenceObligations, plancontract.SourceEvidenceObligation{Path: path, Kind: "featureplanner_evidence", Required: true, Source: "featureplanner"})
	}
	return contract.Normalized()
}

func featurePlannerTDDObligationsByID(obligations []TDDObligation) map[string]TDDObligation {
	out := map[string]TDDObligation{}
	for _, obligation := range obligations {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			out[id] = obligation
		}
	}
	return out
}

func atomEditSetObligations(atom FeatureAtomPlan, required []string) plancontract.EditSetObligations {
	var obligations plancontract.EditSetObligations
	for _, raw := range atom.EditSetObligations {
		switch plancontract.EditSetObligationKind(strings.TrimSpace(raw)) {
		case plancontract.EditSetObligationKindTestEdit:
			obligations = obligations.With(plancontract.EditSetObligationKindTestEdit, "featureplanner.atom_contract")
		case plancontract.EditSetObligationKindImplementationEdit:
			obligations = obligations.With(plancontract.EditSetObligationKindImplementationEdit, "featureplanner.atom_contract")
		}
	}
	if len(atom.TestFirst.Commands) > 0 || len(atom.TestFirst.ObligationRefs) > 0 {
		obligations = obligations.With(plancontract.EditSetObligationKindTestEdit, "featureplanner.tdd_contract")
	}
	for _, path := range required {
		if plancontract.PathLooksLikeImplementation(path) {
			obligations = obligations.With(plancontract.EditSetObligationKindImplementationEdit, "featureplanner.required_edit_paths")
			break
		}
	}
	return obligations.Normalized()
}

func atomStopConditions(atom FeatureAtomPlan) []string {
	var out []string
	if !atom.ImplementationRequired {
		out = append(out, "implementation_required_after_characterization=false")
	}
	return out
}

func StampPlanAuthorityContract(plan *FeaturePlan, deltas []PlanAuthorityDelta) plancontract.PlanExecutionContract {
	if plan == nil {
		return plancontract.PlanExecutionContract{}
	}
	contract := CompilePlanAuthorityContract(*plan, deltas)
	byAtom := map[string]plancontract.AtomExecutionContract{}
	for _, atomContract := range contract.Atoms {
		byAtom[atomContract.AtomID] = atomContract
	}
	for i := range plan.Atoms {
		atomID := plancontract.NormalizeStableContractIDPart(plan.Atoms[i].ID)
		atomContract, ok := byAtom[atomID]
		if !ok {
			continue
		}
		plan.Atoms[i].RuntimeEnvelope.ContractID = atomContract.Scope.ContractID
		plan.Atoms[i].RuntimeEnvelope.ProtocolHash = contract.ContractHash
	}
	return contract
}

func planAuthorityContractAtom(contract plancontract.PlanExecutionContract, atomID string) (plancontract.AtomExecutionContract, bool) {
	atomID = plancontract.NormalizeStableContractIDPart(atomID)
	for _, atom := range contract.Atoms {
		if atom.AtomID == atomID {
			return atom, true
		}
	}
	return plancontract.AtomExecutionContract{}, false
}

func planAuthorityContractNewFileAuthorized(contract plancontract.PlanExecutionContract, path string) bool {
	path = plancontract.NormalizeScopePath(path)
	if path == "" {
		return false
	}
	for _, candidate := range contract.AuthorizedNewFiles {
		if plancontract.NormalizeScopePath(candidate.Path) == path {
			return true
		}
	}
	for _, root := range contract.AuthorizedNewRoots {
		normalizedRoot := plancontract.NormalizeScopePath(root.Root)
		if normalizedRoot != "" && (path == normalizedRoot || strings.HasPrefix(path, normalizedRoot+"/")) {
			return true
		}
	}
	return false
}

func newFileAuthorizationsFromDeltas(deltas []PlanAuthorityDelta) []plancontract.AuthorizedNewFile {
	var files []plancontract.AuthorizedNewFile
	for _, delta := range normalizePlanAuthorityDeltas(deltas) {
		if !deltaAuthorizesNewFile(delta) {
			continue
		}
		for _, path := range delta.AffectedPaths {
			files = append(files, plancontract.AuthorizedNewFile{
				Path:   path,
				Reason: firstNonEmpty(delta.Actual, delta.Expected),
				Source: delta.AuthorityKind,
				Metadata: plancontract.NewFileAuthorizationMetadata{
					Compiler: "featureplanner.typed_authority_delta",
					Rule:     delta.QuestionID,
				},
			})
		}
	}
	return files
}

func StandalonePromptNewFileAuthorityDeltas(plan FeaturePlan, prompt string, workspaceRoot string, now time.Time) []PlanAuthorityDelta {
	paths := standalonePromptAuthorizedNewFilePaths(plan, prompt, workspaceRoot)
	if len(paths) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return []PlanAuthorityDelta{{
		QuestionID:     "operator-prompt-new-files",
		AuthorityKind:  "operator_approval",
		EvidenceRefs:   []string{"operator_prompt"},
		AffectedPaths:  paths,
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "missing new files require explicit operator authorization",
		Actual:         "operator prompt explicitly authorized new-file or package creation work",
		Revision:       1,
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
	}}
}

func standalonePromptAuthorizedNewFilePaths(plan FeaturePlan, prompt string, workspaceRoot string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, atom := range plan.Atoms {
		for _, scopedPath := range append(standaloneAtomWorkspaceScopePaths(atom), standaloneAtomTestArtifactPaths(atom)...) {
			path := plancontract.NormalizeScopePath(scopedPath)
			if path == "" || seen[path] || !plancontract.NewFileAuthorizedForPath(prompt, path) {
				continue
			}
			if workspaceRoot != "" && standaloneWorkspacePathExists(workspaceRoot, path) {
				continue
			}
			paths = append(paths, path)
			seen[path] = true
		}
	}
	return normalizeEditScopeList(paths)
}

func standaloneWorkspacePathExists(root string, path string) bool {
	abs := filepath.Join(root, filepath.FromSlash(path))
	if _, err := os.Stat(abs); err == nil {
		return true
	}
	return false
}

func deltaAuthorizesNewFile(delta PlanAuthorityDelta) bool {
	switch delta.AuthorityKind {
	case "new_file_authorization", "operator_approval":
	default:
		return false
	}
	return len(delta.AffectedPaths) > 0 &&
		(len(delta.AffectedFields) == 0 ||
			deltaAllowsField(delta, "new_file_authorization") ||
			deltaAllowsField(delta, "required_edit_paths"))
}

func planContractAuthorityDeltas(deltas []PlanAuthorityDelta) []plancontract.AuthorityDelta {
	var out []plancontract.AuthorityDelta
	for _, delta := range normalizePlanAuthorityDeltas(deltas) {
		kind := planContractAuthorityDeltaKind(delta)
		for _, path := range append([]string(nil), delta.AffectedPaths...) {
			out = append(out, plancontract.AuthorityDelta{
				Kind:     kind,
				Path:     path,
				From:     delta.Expected,
				To:       delta.Actual,
				Reason:   strings.Join(delta.EvidenceRefs, ", "),
				Source:   delta.AuthorityKind,
				Approved: true,
			})
		}
		if len(delta.AffectedPaths) == 0 {
			out = append(out, plancontract.AuthorityDelta{
				Kind:     kind,
				From:     delta.Expected,
				To:       delta.Actual,
				Reason:   strings.Join(delta.EvidenceRefs, ", "),
				Source:   delta.AuthorityKind,
				Approved: true,
			})
		}
	}
	return out
}

func planContractAuthorityDeltaKind(delta PlanAuthorityDelta) plancontract.AuthorityDeltaKind {
	if deltaAuthorizesNewFile(delta) {
		return plancontract.AuthorityDeltaKindAddNewFile
	}
	for _, field := range delta.AffectedFields {
		switch field {
		case "required_edit_paths":
			return plancontract.AuthorityDeltaKindPromoteRequiredPath
		case "write_allowed_paths":
			return plancontract.AuthorityDeltaKindExpandWriteEnvelope
		}
	}
	return plancontract.AuthorityDeltaKindExpandWriteEnvelope
}

func firstNonEmptyStringSlice(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func atomRequiredTestCommands(atom plancontract.AtomExecutionContract) []string {
	var out []string
	for _, test := range atom.RequiredTests {
		out = append(out, test.Command)
	}
	return normalizedStringList(out)
}

func atomTDDArtifactPaths(atom plancontract.AtomExecutionContract) []string {
	var out []string
	for _, obligation := range atom.TDDObligations {
		out = append(out, obligation.TestArtifactPaths...)
	}
	return normalizeEditScopeList(out)
}

func atomAcceptanceRefs(atom plancontract.AtomExecutionContract) []string {
	var out []string
	for _, obligation := range atom.AcceptanceObligations {
		out = append(out, firstNonEmpty(obligation.ID, obligation.Description))
	}
	return normalizedStringList(out)
}

func atomSourceEvidenceRefs(atom plancontract.AtomExecutionContract) []string {
	var out []string
	for _, obligation := range atom.SourceEvidenceObligations {
		out = append(out, firstNonEmpty(obligation.ID, obligation.Path))
	}
	return normalizedStringList(out)
}

func ValidateSubmittedPlanAgainstLockedContract(previous FeaturePlan, submitted FeaturePlan, deltas []PlanAuthorityDelta) error {
	if len(previous.Atoms) == 0 {
		return nil
	}
	locked := CompilePlanAuthorityContract(previous, nil)
	next := CompilePlanAuthorityContract(submitted, deltas)
	authority := planAuthorityDeltaIndex(deltas)
	var errs []error
	nextAtoms := map[string]plancontract.AtomExecutionContract{}
	for _, atom := range next.Atoms {
		nextAtoms[atom.AtomID] = atom
	}
	for _, atom := range locked.Atoms {
		candidate, ok := nextAtoms[atom.AtomID]
		if !ok {
			if !authority.authorizesField(atom.AtomID, "atom_identity", "") {
				errs = append(errs, fmt.Errorf("locked atom %q is missing from submitted plan without typed authority delta", atom.AtomID))
			}
			continue
		}
		errs = append(errs, validateLockedStrings(atom.AtomID, "required_edit_paths", atom.Scope.RequiredEditPaths(), candidate.Scope.RequiredEditPaths(), authority)...)
		errs = append(errs, validateLockedStrings(atom.AtomID, "allowed_edit_paths", atom.Scope.AllowedEditPaths(), candidate.Scope.AllowedEditPaths(), authority)...)
		errs = append(errs, validateLockedStrings(atom.AtomID, "required_tests", atomRequiredTestCommands(atom), atomRequiredTestCommands(candidate), authority)...)
		errs = append(errs, validateLockedStrings(atom.AtomID, "test_artifact_paths", atomTDDArtifactPaths(atom), atomTDDArtifactPaths(candidate), authority)...)
		errs = append(errs, validateLockedStrings(atom.AtomID, "acceptance_refs", atomAcceptanceRefs(atom), atomAcceptanceRefs(candidate), authority)...)
		errs = append(errs, validateLockedStrings(atom.AtomID, "source_evidence_refs", atomSourceEvidenceRefs(atom), atomSourceEvidenceRefs(candidate), authority)...)
	}
	lockedIDs := map[string]bool{}
	for _, atom := range locked.Atoms {
		lockedIDs[atom.AtomID] = true
	}
	for _, atom := range next.Atoms {
		if !lockedIDs[atom.AtomID] && !authority.authorizesField(atom.AtomID, "atom_identity", "") {
			errs = append(errs, fmt.Errorf("submitted plan adds atom %q without typed authority delta", atom.AtomID))
		}
	}
	return errors.Join(errs...)
}

func validateLockedStrings(atomID string, field string, locked []string, submitted []string, authority planAuthorityDeltaIndex) []error {
	var errs []error
	submittedSet := normalizedStringSet(submitted)
	for _, value := range normalizedStringList(locked) {
		if submittedSet[value] {
			continue
		}
		if authority.authorizesField(atomID, field, value) {
			continue
		}
		errs = append(errs, fmt.Errorf("locked atom %q %s value %q was rewritten or demoted without typed authority delta", atomID, field, value))
	}
	lockedSet := normalizedStringSet(locked)
	for _, value := range normalizedStringList(submitted) {
		if lockedSet[value] {
			continue
		}
		if authority.authorizesField(atomID, field, value) {
			continue
		}
		errs = append(errs, fmt.Errorf("locked atom %q %s value %q was added without typed authority delta", atomID, field, value))
	}
	return errs
}

type planAuthorityDeltaIndex []PlanAuthorityDelta

func (index planAuthorityDeltaIndex) authorizesField(atomID string, field string, value string) bool {
	field = strings.TrimSpace(field)
	value = strings.TrimSpace(value)
	for _, delta := range index {
		if !deltaAppliesToAtom(delta, atomID) {
			continue
		}
		if len(delta.AffectedFields) > 0 && !deltaAllowsField(delta, field) {
			continue
		}
		if value != "" && len(delta.AffectedPaths) > 0 && !containsString(delta.AffectedPaths, value) {
			continue
		}
		return true
	}
	return false
}

func deltaAppliesToAtom(delta PlanAuthorityDelta, atomID string) bool {
	if strings.TrimSpace(atomID) == "" {
		return true
	}
	for _, field := range delta.AffectedFields {
		if strings.TrimSpace(field) == "atom:"+atomID {
			return true
		}
	}
	return len(delta.AffectedFields) == 0
}

func deltaAllowsField(delta PlanAuthorityDelta, field string) bool {
	for _, candidate := range delta.AffectedFields {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == field {
			return true
		}
		if strings.HasPrefix(candidate, "atom:") {
			continue
		}
	}
	return false
}

func normalizePlanAuthorityDeltas(deltas []PlanAuthorityDelta) []PlanAuthorityDelta {
	out := make([]PlanAuthorityDelta, 0, len(deltas))
	for _, delta := range deltas {
		delta.QuestionID = strings.TrimSpace(delta.QuestionID)
		delta.AuthorityKind = strings.TrimSpace(delta.AuthorityKind)
		delta.EvidenceRefs = normalizedStringList(delta.EvidenceRefs)
		delta.AffectedPaths = normalizeEditScopeList(delta.AffectedPaths)
		delta.AffectedFields = normalizedStringList(delta.AffectedFields)
		delta.Expected = strings.TrimSpace(delta.Expected)
		delta.Actual = strings.TrimSpace(delta.Actual)
		delta.CreatedAt = strings.TrimSpace(delta.CreatedAt)
		if delta.QuestionID == "" || delta.AuthorityKind == "" || len(delta.EvidenceRefs) == 0 || delta.Revision <= 0 || delta.CreatedAt == "" {
			continue
		}
		out = append(out, delta)
	}
	return out
}

func newFileAuthorizedPathsFromDeltas(deltas []PlanAuthorityDelta) []string {
	var paths []string
	for _, delta := range normalizePlanAuthorityDeltas(deltas) {
		switch delta.AuthorityKind {
		case "new_file_authorization", "operator_approval":
		default:
			continue
		}
		if len(delta.AffectedPaths) == 0 {
			continue
		}
		if len(delta.AffectedFields) > 0 && !deltaAllowsField(delta, "new_file_authorization") && !deltaAllowsField(delta, "required_edit_paths") {
			continue
		}
		paths = append(paths, delta.AffectedPaths...)
	}
	return normalizeEditScopeList(paths)
}

func NormalizePlanProductWorkflowContext(plan FeaturePlan, brief ProductWorkflowBrief, workspaceRoot string) FeaturePlan {
	grounding := newRepoGrounding(brief, workspaceRoot)
	for i := range plan.Atoms {
		atom := &plan.Atoms[i]
		required := normalizeEditScopeList(atom.RequiredEditPaths)
		requiredSet := normalizedStringSet(required)
		allowed := normalizeEditScopeList(atom.AllowedEditPaths)
		for _, path := range productAtomRequiredEditPaths(plan, brief, *atom, allowed) {
			if requiredSet[path] || !grounding.pathGrounded(path) {
				continue
			}
			required = append(required, path)
			requiredSet[path] = true
		}
		keptAllowed := make([]string, 0, len(allowed))
		for _, path := range allowed {
			if requiredSet[path] {
				continue
			}
			if grounding.pathGrounded(path) {
				keptAllowed = append(keptAllowed, path)
			}
		}
		atom.RequiredEditPaths = required
		atom.AllowedEditPaths = normalizeEditScopeList(keptAllowed)
	}
	return plan
}

func productAtomRequiredEditPaths(plan FeaturePlan, brief ProductWorkflowBrief, atom FeatureAtomPlan, allowed []string) []string {
	paths := productAtomRequiredTestArtifactPaths(atom)
	filesToModify := normalizeEditScopeList(brief.FilesToModify)
	if len(plan.Atoms) == 1 {
		paths = append(paths, filesToModify...)
	} else {
		filesToModifySet := normalizedStringSet(filesToModify)
		for _, path := range allowed {
			if filesToModifySet[path] {
				paths = append(paths, path)
			}
		}
	}
	return normalizeEditScopeList(paths)
}

func productAtomRequiredTestArtifactPaths(atom FeatureAtomPlan) []string {
	return requiredTDDTestArtifactPaths(atom)
}

func requiredTDDTestArtifactPaths(atom FeatureAtomPlan) []string {
	var paths []string
	if atom.TestFirst.BaselineRequired ||
		len(atom.TestFirst.ObligationRefs) > 0 ||
		len(atom.TDDObligations) > 0 ||
		util.EqualTrimFold(atom.TestFirst.ExpectedPreExecutionResult, "red-confirmed") {
		paths = append(paths, atom.TestFirst.TestArtifactPaths...)
	}
	if len(atom.PlannedTDDSnapshot.ObligationIDs) > 0 ||
		len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) > 0 {
		paths = append(paths, atom.PlannedTDDSnapshot.TestArtifactPaths...)
	}
	return normalizeEditScopeList(paths)
}

func validatePlanCoversFilesToModify(plan FeaturePlan, brief ProductWorkflowBrief) []error {
	filesToModify := normalizeEditScopeList(brief.FilesToModify)
	if len(filesToModify) == 0 {
		return nil
	}
	requiredByAnyAtom := map[string]bool{}
	allowedByAnyAtom := map[string]bool{}
	for _, atom := range plan.Atoms {
		for _, path := range normalizeEditScopeList(atom.RequiredEditPaths) {
			requiredByAnyAtom[path] = true
		}
		for _, path := range normalizeEditScopeList(atom.AllowedEditPaths) {
			allowedByAnyAtom[path] = true
		}
	}
	var errs []error
	for _, path := range filesToModify {
		if allowedByAnyAtom[path] {
			errs = append(errs, fmt.Errorf("product workflow Files to modify path %q must not appear in atom.allowed_edit_paths; Files to modify are required implementation scope", path))
		}
		if !requiredByAnyAtom[path] {
			errs = append(errs, fmt.Errorf("product workflow Files to modify path %q must be covered by atom.required_edit_paths, not omitted or demoted to allowed_edit_paths", path))
		}
	}
	return errs
}

func normalizeEditScopeList(paths []string) []string {
	return plancontract.UniqueScopePaths(paths)
}

type productRepoGrounding struct {
	workspaceRoot             string
	filesToModify             map[string]bool
	authorizedNewPackageRoots map[string]bool
	packageRoots              map[string]bool
	hasLocalization           bool
}

func newRepoGrounding(brief ProductWorkflowBrief, workspaceRoot string) productRepoGrounding {
	grounding := productRepoGrounding{
		workspaceRoot:             strings.TrimSpace(workspaceRoot),
		filesToModify:             normalizedPathSet(brief.FilesToModify),
		authorizedNewPackageRoots: normalizedPathSet(brief.AuthorizedNewPackageRoots),
		packageRoots:              map[string]bool{},
	}
	grounding.hasLocalization = len(grounding.filesToModify) > 0 || len(grounding.authorizedNewPackageRoots) > 0
	for path := range grounding.filesToModify {
		grounding.addExistingPackageRoot(path)
	}
	return grounding
}

func (g *productRepoGrounding) addExistingPackageRoot(path string) {
	dir := normalizedPathDir(path)
	if dir == "" || dir == "." {
		return
	}
	if existingPackageDir(g.workspaceRoot, dir) {
		g.packageRoots[dir] = true
	}
}

func (g productRepoGrounding) validateAtomEditScope(atomIndex int, atom FeatureAtomPlan) []error {
	var errs []error
	for _, scopedPath := range append(normalizedStringList(atom.RequiredEditPaths), normalizedStringList(atom.AllowedEditPaths)...) {
		if plancontract.NormalizeScopePath(scopedPath) == "" {
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
		errs = append(errs, fmt.Errorf("atom %d edit path %q is ungrounded by files_to_modify, existing files_to_modify package roots, or authorized new package roots; use ask_planner_question for planning repair", atomIndex, scopedPath))
	}
	return errs
}

func (g productRepoGrounding) validateAtomTestArtifactScope(atomIndex int, atom FeatureAtomPlan) []error {
	var errs []error
	paths := normalizedStringList(append(atom.TestFirst.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths...))
	for _, scopedPath := range paths {
		if plancontract.NormalizeScopePath(scopedPath) == "" {
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
		errs = append(errs, fmt.Errorf("atom %d test artifact path %q is ungrounded by files_to_modify, existing files_to_modify package roots, or authorized new package roots; use ask_planner_question for planning repair", atomIndex, scopedPath))
	}
	return errs
}

func (g productRepoGrounding) pathGrounded(path string) bool {
	path = plancontract.NormalizeScopePath(path)
	if path == "" {
		return false
	}
	if g.filesToModify[path] {
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

func existingPackageDir(workspaceRoot string, dir string) bool {
	entries, err := os.ReadDir(repoRelativePath(workspaceRoot, dir))
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

func repoRelativePath(workspaceRoot string, path string) string {
	path = filepath.FromSlash(path)
	if strings.TrimSpace(workspaceRoot) != "" {
		return filepath.Join(workspaceRoot, path)
	}
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
	path = plancontract.NormalizeScopePath(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
}

func pathWithinAnyRoot(path string, roots map[string]bool) bool {
	path = plancontract.NormalizeScopePath(path)
	if path == "" {
		return false
	}
	for root := range roots {
		root = strings.TrimRight(plancontract.NormalizeScopePath(root), "/")
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

func normalizedPathSet(paths []string) map[string]bool {
	out := map[string]bool{}
	for _, path := range plancontract.UniqueScopePaths(paths) {
		out[path] = true
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
	for i, delta := range state.AuthorityDeltas {
		if strings.TrimSpace(delta.QuestionID) == "" {
			errs = append(errs, fmt.Errorf("authority delta %d missing question_id", i))
		}
		if strings.TrimSpace(delta.AuthorityKind) == "" {
			errs = append(errs, fmt.Errorf("authority delta %d missing authority_kind", i))
		}
		if len(normalizedStringList(delta.EvidenceRefs)) == 0 {
			errs = append(errs, fmt.Errorf("authority delta %d missing evidence_refs", i))
		}
		if len(delta.AffectedPaths) > 0 && len(normalizeEditScopeList(delta.AffectedPaths)) != len(delta.AffectedPaths) {
			errs = append(errs, fmt.Errorf("authority delta %d has invalid affected_paths", i))
		}
		if delta.Revision <= 0 {
			errs = append(errs, fmt.Errorf("authority delta %d missing revision", i))
		}
		if strings.TrimSpace(delta.CreatedAt) == "" {
			errs = append(errs, fmt.Errorf("authority delta %d missing created_at", i))
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
	case ToolSourceSatisfied:
		closeout := normalizeSourceSatisfiedCloseout(parsed.SourceSatisfied)
		if err := ValidateSourceSatisfiedCloseout(closeout); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return []Event{{Type: "idiom.feature_planner.source_satisfied_rejected", SessionID: state.SessionID, Message: err.Error()}}
		}
		state.Status = FeatureStateSourceSatisfied
		state.PlanningLoop.State = FeatureStateSourceSatisfied
		state.Blockers = nil
		state.OpenQuestions = nil
		state.OpenRecoveryPackets = nil
		state.DerivedNextValidActions = DeriveNextValidActions(*state)
		fields := map[string]any{
			"summary":        closeout.Summary,
			"evidence_paths": closeout.EvidencePaths,
		}
		if closeout.Reason != "" {
			fields["reason"] = closeout.Reason
		}
		if len(closeout.TestPaths) > 0 {
			fields["test_paths"] = closeout.TestPaths
		}
		if len(closeout.Anchors) > 0 {
			fields["anchors"] = closeout.Anchors
		}
		return []Event{{Type: EventPlannerSourceSatisfied, SessionID: state.SessionID, FeatureTo: FeatureStateSourceSatisfied, Message: closeout.Summary, Fields: fields}}
	case ToolSubmitFeaturePlan:
		hydrateDurablePlanDefaults(&parsed.Plan, state, now)
		StampPlanAuthorityContract(&parsed.Plan, state.AuthorityDeltas)
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

func normalizeSourceSatisfiedCloseout(closeout SourceSatisfiedCloseout) SourceSatisfiedCloseout {
	closeout.Tool = ToolSourceSatisfied
	closeout.Summary = strings.TrimSpace(closeout.Summary)
	closeout.Reason = strings.TrimSpace(closeout.Reason)
	closeout.EvidencePaths = nonEmptyFeaturePlannerStrings(closeout.EvidencePaths)
	closeout.TestPaths = nonEmptyFeaturePlannerStrings(closeout.TestPaths)
	closeout.Anchors = nonEmptySourceSatisfiedAnchors(closeout.Anchors)
	return closeout
}

func nonEmptySourceSatisfiedAnchors(values []SourceSatisfiedAnchor) []SourceSatisfiedAnchor {
	if len(values) == 0 {
		return nil
	}
	out := make([]SourceSatisfiedAnchor, 0, len(values))
	for _, value := range values {
		value.Path = strings.TrimSpace(value.Path)
		value.Text = strings.TrimSpace(value.Text)
		if value.Path == "" || value.Text == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func PreserveSourceSatisfiedGate(plan *FeaturePlan, prompt string) {
	if plan == nil || !promptRequiresSourceSatisfiedGate(prompt) {
		return
	}
	for i := range plan.Atoms {
		atomPrompt := strings.TrimSpace(plan.Atoms[i].Prompt)
		if promptCarriesSourceSatisfiedGate(atomPrompt) {
			continue
		}
		prefix := "Before editing, verify whether the current source/test evidence already satisfies the request. If it does, report Decision: source-satisfied with current-state anchors and do not edit. Only implement if that verification proves a real gap."
		if atomPrompt == "" {
			plan.Atoms[i].Prompt = prefix
			continue
		}
		plan.Atoms[i].Prompt = prefix + "\n\n" + atomPrompt
	}
}

func promptRequiresSourceSatisfiedGate(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "source-satisfied") &&
		(strings.Contains(lower, "do not edit") || strings.Contains(lower, "don't edit") || strings.Contains(lower, "already satisfied"))
}

func promptCarriesSourceSatisfiedGate(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "source-satisfied") &&
		(strings.Contains(lower, "do not edit") || strings.Contains(lower, "don't edit"))
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

func InvalidStrictPreDispatchTDD(atom FeatureAtomPlan) []string {
	var invalid []string
	if reason := invalidPlannedTDDSnapshotIDReason(atom.PlannedTDDSnapshot.SnapshotID); reason != "" {
		invalid = append(invalid, "planned_tdd_snapshot.snapshot_id "+reason)
	}
	if reason := invalidPlannedTDDSnapshotDigestReason(atom.PlannedTDDSnapshot.SnapshotDigest); reason != "" {
		invalid = append(invalid, "planned_tdd_snapshot.snapshot_digest "+reason)
	}
	return invalid
}

func invalidPlannedTDDSnapshotIDReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.Contains(value, "...") {
		return "contains an ellipsis placeholder; use the snapshot_id from harness TDD evidence"
	}
	for _, token := range []string{"placeholder", "fake"} {
		if strings.Contains(lower, token) {
			return fmt.Sprintf("contains placeholder token %q; use the snapshot_id from harness TDD evidence", token)
		}
	}
	if strings.HasPrefix(lower, "snap-tdd-") {
		return "uses the fake snap-tdd-* placeholder pattern; use the snapshot_id from harness TDD evidence"
	}
	return ""
}

func invalidPlannedTDDSnapshotDigestReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.Contains(value, "...") {
		return "contains an ellipsis placeholder; must be sha256:<64 lowercase hex> from harness TDD evidence"
	}
	for _, token := range []string{"placeholder", "fake", "test", "todo", "tbd", "example"} {
		if strings.Contains(lower, token) {
			return fmt.Sprintf("contains placeholder token %q; must be sha256:<64 lowercase hex> from harness TDD evidence", token)
		}
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return "must be sha256:<64 lowercase hex> from harness TDD evidence"
	}
	hexPart := strings.TrimPrefix(value, prefix)
	if len(hexPart) != 64 {
		return fmt.Sprintf("must be sha256:<64 lowercase hex>; got %d characters after sha256:", len(hexPart))
	}
	for _, r := range hexPart {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return "must be sha256:<64 lowercase hex>; digest contains non-lowercase-hex characters"
	}
	return ""
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
	case FeatureStateReleased, FeatureStateClosedWithRisk, FeatureStateCanceled, FeatureStateComplete, FeatureStateSourceSatisfied:
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
