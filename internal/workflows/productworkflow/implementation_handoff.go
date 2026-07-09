package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
)

type ImplementationHandoff struct {
	EffortID                  string
	TaskID                    string
	EffortRoot                string
	ArtifactRootPath          string
	StatusBucket              string
	CurrentPhase              string
	CurrentState              string
	NextValidActions          []string
	CoverageTier              string
	CoverageGatePassed        bool
	CoverageContract          *ImplementationHandoffCoverageContract
	AcceptedCoverage          *ImplementationHandoffAcceptedCoverage
	CoverageBlockers          []string
	CoverageBlockerKinds      []string
	PRDRequirements           []ImplementationHandoffPRDRequirement
	AcceptedADRIDs            []string
	AcceptedADRPaths          []string
	TDDObligations            []ImplementationHandoffTDDObligation
	FilesToModify             []string
	FileActions               map[string]string
	AuthorizedNewPackageRoots []string
	ContextEvidencePaths      []string
	RequiredBehavior          []string
	Tests                     []string
	Acceptance                []string
	Constraints               []string
	Decisions                 []string
	Blockers                  []string
}

type ImplementationHandoffCoverageContract struct {
	ContractID   string
	Digest       string
	EventID      string
	Status       string
	EffortID     string
	CoverageTier string
}

type ImplementationHandoffAcceptedCoverage struct {
	Contract   ImplementationHandoffCoverageContract
	Projection AcceptedBehavioralCoverageProjection
	Behavioral BehavioralCoverageContract
	Projected  TDDCoverage
	Status     string
	Authority  string
}

type ImplementationHandoffPRDRequirement struct {
	ID          string
	Priority    string
	Statement   string
	Testability string
	Metric      string
	Obligations []string
}

type ImplementationHandoffTDDObligation struct {
	ID             string
	Behavior       string
	Command        string
	RequiredBefore string
	Status         string
}

type CoverageGateEffortEvaluation struct {
	Evaluation CoverageGateEvaluation
	TDD        TDDLedger
	Findings   []TDDObligationPlanFinding
}

func LoadImplementationHandoff(effortRoot string) (ImplementationHandoff, error) {
	input, blockers := LoadBriefGenerationInput(effortRoot)
	if parseBlockers := briefGenerationInputParseBlockers(blockers); len(parseBlockers) > 0 {
		return ImplementationHandoff{}, fmt.Errorf("%s", strings.Join(parseBlockers, "; "))
	}

	return BuildImplementationHandoff(input), nil
}

func LoadBriefGenerationInput(effortRoot string) (BriefGenerationInput, []string) {
	root := filepath.Clean(effortRoot)
	input := BriefGenerationInput{
		EffortRoot:       root,
		ArtifactRootPath: artifactRootPathForEffort(root),
	}
	var blockers []string

	readAndParse := func(relPath string, parse func([]byte) error) {
		data, err := os.ReadFile(filepath.Join(root, relPath))
		if err != nil {
			if os.IsNotExist(err) {
				blockers = append(blockers, fmt.Sprintf("required file missing: %s", filepath.ToSlash(relPath)))
			} else {
				blockers = append(blockers, fmt.Sprintf("read %s: %v", filepath.ToSlash(relPath), err))
			}

			return
		}

		if err := parse(data); err != nil {
			blockers = append(blockers, fmt.Sprintf("parse %s: %v", filepath.ToSlash(relPath), err))
		}
	}

	readAndParse(WorkflowStateFileName, func(data []byte) error {
		parsed, err := ParseWorkflowStateJSON(data)
		if err == nil {
			input.WorkflowState = parsed
		}

		return err
	})
	readAndParse("prd.md", func(data []byte) error {
		parsed, err := ParsePRDMarkdown(data)
		if err == nil {
			input.PRD = parsed
		}

		return err
	})
	readAndParse(filepath.Join("adrs", "index.json"), func(data []byte) error {
		parsed, err := ParseADRIndexJSON(data)
		if err == nil {
			input.ADRIndex = parsed
		}

		return err
	})

	for _, entry := range input.ADRIndex.ADRs {
		rel := filepath.Join("adrs", entry.Path)
		readAndParse(rel, func(data []byte) error {
			parsed, err := ParseADRMarkdown(data)
			if err == nil {
				input.ADRs = append(input.ADRs, ParsedADR{Doc: parsed, RelPath: rel, RawMarkdown: string(data)})
			}

			return err
		})
	}

	readAndParse("tdd.json", func(data []byte) error {
		parsed, err := ParseTDDLedgerJSON(data)
		if err == nil {
			input.TDD = parsed
		}

		return err
	})
	readAndParse(filepath.Join("evidence", "traceability_matrix.json"), func(data []byte) error {
		parsed, err := ParseTraceabilityMatrixJSON(data)
		if err == nil {
			input.Traceability = parsed
		}

		return err
	})
	readAndParse("implementation_ledger.jsonl", func(data []byte) error {
		parsed, err := ParseImplementationLedgerJSONL(data)
		if err == nil {
			input.ImplementationRecords = parsed
		}

		return err
	})
	if data, err := os.ReadFile(filepath.Join(root, "events.jsonl")); err == nil {
		parsed, parseErr := ParseBehavioralCoveragePromotionEventsJSONL(data)
		if parseErr != nil {
			blockers = append(blockers, fmt.Sprintf("parse events.jsonl: %v", parseErr))
		} else {
			input.PromotionEvents = parsed
		}
	} else if err != nil && !os.IsNotExist(err) {
		blockers = append(blockers, fmt.Sprintf("read events.jsonl: %v", err))
	}

	input = PrepareBriefGenerationInput(input)
	if input.ExistingWorkspacePath == nil {
		input.ExistingWorkspacePath = implementationHandoffExistingWorkspacePaths(input.EffortRoot, input.WorkflowState, input.ImplementationRecords)
	}
	if len(blockers) == 0 && CoverageGateHasBlockers(input.CoverageEvaluation) {
		blockers = append(blockers, "coverage gate evaluation is blocked")
	}

	return input, blockers
}

func LoadCoverageGateEvaluation(effortRoot string) CoverageGateEffortEvaluation {
	input, blockers := LoadBriefGenerationInput(effortRoot)
	eval := input.CoverageEvaluation
	if strings.TrimSpace(string(eval.Status)) == "" {
		eval = CoverageGateEvaluation{
			EffortID:     input.TDD.EffortID,
			TaskID:       input.TDD.TaskID,
			CoverageTier: input.TDD.CoverageTier,
			Status:       ProductWorkflowStatusBlocked,
			Criteria:     []CoverageGateCriterion{},
			Findings:     []CoverageGateFinding{},
			Blockers:     []CoverageGateBlocker{},
		}
	}

	var findings []TDDObligationPlanFinding
	for _, blocker := range blockers {
		trimmed := strings.TrimSpace(blocker)
		if trimmed == "" || trimmed == "coverage gate evaluation is blocked" {
			continue
		}
		kind, file, message := coverageGateInputBlockerParts(trimmed)
		findings = append(findings, TDDObligationPlanFinding{
			Kind:     kind,
			Message:  message,
			File:     file,
			Severity: ProductWorkflowSeverityError,
		})
		eval.Findings = append(eval.Findings, CoverageGateFinding{
			Kind:      kind,
			Criterion: "input",
			Message:   message,
			File:      file,
			Severity:  ProductWorkflowSeverityError,
		})
		eval.Blockers = append(eval.Blockers, CoverageGateBlocker{
			Kind:      kind,
			Criterion: "input",
			Message:   message,
			File:      file,
		})
	}
	if len(findings) > 0 {
		eval.Status = ProductWorkflowStatusBlocked
	}

	return CoverageGateEffortEvaluation{
		Evaluation: eval,
		TDD:        input.TDD,
		Findings:   findings,
	}
}

func coverageGateInputBlockerParts(blocker string) (kind string, file string, message string) {
	message = strings.TrimSpace(blocker)
	kind = "input_error"
	if before, after, ok := strings.Cut(message, ":"); ok {
		prefix := strings.TrimSpace(before)
		message = strings.TrimSpace(after)
		switch {
		case prefix == "required file missing":
			kind = "missing_file"
			file = message
			message = "not found"
		case strings.HasPrefix(prefix, "required file missing "):
			kind = "missing_file"
			file = strings.TrimSpace(strings.TrimPrefix(prefix, "required file missing "))
		case strings.HasPrefix(prefix, "parse "):
			kind = "parse_error"
			file = strings.TrimSpace(strings.TrimPrefix(prefix, "parse "))
		case strings.HasPrefix(prefix, "read "):
			kind = "read_error"
			file = strings.TrimSpace(strings.TrimPrefix(prefix, "read "))
		}
	}
	file = strings.Trim(file, " :")
	if message == "" {
		message = blocker
	}

	return kind, file, message
}

func briefGenerationInputParseBlockers(blockers []string) []string {
	var out []string
	for _, blocker := range blockers {
		trimmed := strings.TrimSpace(blocker)
		if trimmed == "coverage gate evaluation is blocked" {
			continue
		}
		if strings.HasPrefix(trimmed, "parse implementation_ledger.jsonl:") {
			continue
		}
		out = append(out, blocker)
	}

	return out
}

func PrepareBriefGenerationInput(input BriefGenerationInput) BriefGenerationInput {
	input.EffortRoot = strings.TrimSpace(input.EffortRoot)
	if input.ArtifactRootPath == "" && input.EffortRoot != "" {
		input.ArtifactRootPath = artifactRootPathForEffort(input.EffortRoot)
	}
	if strings.TrimSpace(string(input.CoverageEvaluation.Status)) == "" {
		satisfiedTDDIDs := BuildSatisfiedTDDObligationIDs(input.TDD, input.ImplementationRecords)
		if briefGenerationInputHasEffortArtifacts(input.EffortRoot) {
			lintResult := LintEffortFolder(input.EffortRoot)
			input.CoverageEvaluation = EvaluateCoverageGateWithAuthorityPreflight(input.PRD, input.ADRIndex, input.ADRs, input.TDD, input.Traceability, lintResult.Findings, satisfiedTDDIDs)
		} else {
			input.CoverageEvaluation = EvaluateCoverageGate(input.PRD, input.ADRIndex, input.ADRs, input.TDD, input.Traceability, satisfiedTDDIDs)
		}
	}

	return input
}

func artifactRootPathForEffort(effortRoot string) string {
	root := filepath.Clean(effortRoot)
	workspaceRoot := root
	if gitRoot, ok := gitstate.MetadataRoot(root); ok {
		workspaceRoot = gitRoot
		if filepath.IsAbs(gitRoot) {
			if absRoot, err := filepath.Abs(root); err == nil {
				root = absRoot
			}
		}
	}

	if rel, err := filepath.Rel(workspaceRoot, root); err == nil && rel != "." && !strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return filepath.ToSlash(rel)
	}

	return filepath.ToSlash(root)
}

func briefGenerationInputHasEffortArtifacts(effortRoot string) bool {
	root := strings.TrimSpace(effortRoot)
	if root == "" {
		return false
	}
	for _, rel := range []string{WorkflowStateFileName, "prd.md", "tdd.json", filepath.Join("adrs", "index.json")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return true
		}
	}

	return false
}

func BuildImplementationHandoff(input BriefGenerationInput) ImplementationHandoff {
	generated := GenerateImplementationBrief(input)
	coverageTier := strings.TrimSpace(input.CoverageEvaluation.CoverageTier)

	if coverageTier == "" {
		coverageTier = strings.TrimSpace(input.TDD.CoverageTier)
	}

	handoff := ImplementationHandoff{
		EffortID:                  firstNonEmptyHandoffString(input.WorkflowState.EffortID, input.TDD.EffortID, input.CoverageEvaluation.EffortID),
		TaskID:                    firstNonEmptyHandoffString(input.WorkflowState.TaskID, input.TDD.TaskID, input.PRD.FrontMatter.TaskID, input.CoverageEvaluation.TaskID),
		EffortRoot:                strings.TrimSpace(input.EffortRoot),
		ArtifactRootPath:          firstNonEmptyHandoffString(input.ArtifactRootPath, input.EffortRoot),
		StatusBucket:              strings.TrimSpace(input.WorkflowState.StatusBucket),
		CurrentPhase:              strings.TrimSpace(input.WorkflowState.CurrentPhase),
		CurrentState:              strings.TrimSpace(input.WorkflowState.CurrentState),
		NextValidActions:          append([]string{}, input.WorkflowState.NextValidActions...),
		CoverageTier:              coverageTier,
		CoverageGatePassed:        textutil.EqualTrimFold(string(input.CoverageEvaluation.Status), string(ProductWorkflowStatusPass)),
		CoverageContract:          implementationHandoffCoverageContract(input),
		AcceptedCoverage:          implementationHandoffAcceptedCoverage(input),
		CoverageBlockers:          coverageBlockerMessages(input.CoverageEvaluation),
		CoverageBlockerKinds:      coverageBlockerKinds(input.CoverageEvaluation),
		FilesToModify:             append([]string{}, generated.FilesToModify...),
		FileActions:               copyHandoffStringMap(generated.FileActions),
		AuthorizedNewPackageRoots: handoffAuthorizedNewPackageRoots(generated.FileActions),
		ContextEvidencePaths:      briefUniqueSortedStrings(input.WorkflowState.Context.ReferencedPaths),
		RequiredBehavior:          append([]string{}, generated.RequiredBehavior...),
		Tests:                     append([]string{}, generated.Tests...),
		Acceptance:                append([]string{}, generated.Acceptance...),
		Constraints:               append([]string{}, generated.Constraints...),
		Decisions:                 append([]string{}, generated.Decisions...),
		Blockers:                  append([]string{}, generated.Blockers...),
	}
	for _, req := range input.PRD.Requirements {
		id := strings.TrimSpace(req.ID)
		if id == "" {
			continue
		}

		handoff.PRDRequirements = append(handoff.PRDRequirements, ImplementationHandoffPRDRequirement{
			ID:          id,
			Priority:    strings.TrimSpace(req.Priority),
			Statement:   strings.TrimSpace(req.Statement),
			Testability: strings.TrimSpace(req.Testability),
			Metric:      strings.TrimSpace(req.Metric),
			Obligations: briefUniqueSortedStrings(req.TDDObligations),
		})
	}

	for _, entry := range input.ADRIndex.ADRs {
		if !isAcceptedArtifact(entry.Status) {
			continue
		}

		adrID := strings.TrimSpace(entry.ADRID)
		if adrID != "" {
			handoff.AcceptedADRIDs = append(handoff.AcceptedADRIDs, adrID)
		}

		if path := strings.TrimSpace(entry.Path); path != "" {
			handoff.AcceptedADRPaths = append(handoff.AcceptedADRPaths, filepath.ToSlash(filepath.Join("adrs", path)))
		}
	}

	handoff.AcceptedADRIDs = briefUniqueSortedStrings(handoff.AcceptedADRIDs)
	handoff.AcceptedADRPaths = briefUniqueSortedStrings(handoff.AcceptedADRPaths)

	for _, obl := range input.TDD.Obligations {
		id := strings.TrimSpace(obl.ID)
		if id == "" {
			continue
		}

		handoff.TDDObligations = append(handoff.TDDObligations, ImplementationHandoffTDDObligation{
			ID:             id,
			Behavior:       strings.TrimSpace(obl.Behavior),
			Command:        strings.TrimSpace(obl.Command),
			RequiredBefore: strings.TrimSpace(obl.RequiredBefore),
			Status:         strings.TrimSpace(obl.Status),
		})
	}

	return handoff
}

func implementationHandoffCoverageContract(input BriefGenerationInput) *ImplementationHandoffCoverageContract {
	if input.TDD.BehavioralCoverage == nil {
		return nil
	}

	contract := *input.TDD.BehavioralCoverage
	if strings.TrimSpace(contract.EffortID) == "" {
		contract.EffortID = firstNonEmptyHandoffString(input.TDD.EffortID, input.WorkflowState.EffortID, input.CoverageEvaluation.EffortID)
	}
	if strings.TrimSpace(contract.TaskID) == "" {
		contract.TaskID = firstNonEmptyHandoffString(input.TDD.TaskID, input.WorkflowState.TaskID, input.CoverageEvaluation.TaskID)
	}
	if strings.TrimSpace(contract.CoverageTier) == "" {
		contract.CoverageTier = firstNonEmptyHandoffString(input.TDD.CoverageTier, input.CoverageEvaluation.CoverageTier)
	}

	ref := ImplementationHandoffCoverageContract{
		ContractID:   firstNonEmptyHandoffString(contract.ContractID, contract.TaskID, input.TDD.TaskID),
		Digest:       BehavioralCoverageContractDigest(contract),
		Status:       implementationHandoffBehavioralCoverageStatus(contract, input),
		EffortID:     firstNonEmptyHandoffString(contract.EffortID, input.TDD.EffortID, input.WorkflowState.EffortID, input.CoverageEvaluation.EffortID),
		CoverageTier: firstNonEmptyHandoffString(contract.CoverageTier, input.TDD.CoverageTier, input.CoverageEvaluation.CoverageTier),
	}
	ref.EventID = implementationHandoffCoverageEventID(contract, input, ref.Digest)
	if strings.TrimSpace(ref.Status) == "" {
		ref.Status = string(ProductWorkflowStatusBlocked)
	}

	return &ref
}

func implementationHandoffCoverageEventID(contract BehavioralCoverageContract, input BriefGenerationInput, digest string) string {
	if event := AcceptedBehavioralCoveragePromotionEvent(input.PromotionEvents, digest); event != nil {
		return strings.TrimSpace(event.EventID)
	}
	return ""
}

func implementationHandoffBehavioralCoverageStatus(contract BehavioralCoverageContract, input BriefGenerationInput) string {
	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		return string(ProductWorkflowStatusBlocked)
	}
	if projected := ProjectBehavioralCoverageContract(contract); behavioralCoverageHasAcceptedHandoffBlockingGate(projected) {
		return string(ProductWorkflowStatusBlocked)
	}
	if err := ValidateBehavioralCoverageProjection(contract, input.TDD.Coverage); err != nil {
		return string(ProductWorkflowStatusBlocked)
	}
	digest := BehavioralCoverageContractDigest(contract)
	if AcceptedBehavioralCoveragePromotionEvent(input.PromotionEvents, digest) == nil {
		return string(ProductWorkflowStatusBlocked)
	}
	return BehavioralCoverageStatusAccepted
}

func behavioralCoverageHasAcceptedHandoffBlockingGate(coverage TDDCoverage) bool {
	for _, gate := range coverage.GatesBlocked {
		if hasUnavailableMutationBlocker([]string{gate}) {
			continue
		}
		if strings.TrimSpace(gate) != "" {
			return true
		}
	}
	return false
}

func implementationHandoffAcceptedCoverage(input BriefGenerationInput) *ImplementationHandoffAcceptedCoverage {
	projection := ProjectAcceptedBehavioralCoverage(input)
	if projection == nil {
		return nil
	}

	return &ImplementationHandoffAcceptedCoverage{
		Contract:   projection.Contract,
		Projection: *projection,
		Behavioral: projection.Behavioral,
		Projected:  projection.Projected,
		Status:     projection.Status,
		Authority:  projection.Authority,
	}
}

func implementationHandoffAcceptedCoverageStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case BehavioralCoverageStatusAccepted, string(ProductWorkflowStatusPass):
		return true
	default:
		return false
	}
}

func copyHandoffStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}

	return out
}

func implementationHandoffExistingWorkspacePaths(effortRoot string, workflowState WorkflowState, records []ImplementationLedgerRecord) map[string]bool {
	out := map[string]bool{}
	workspaceRoot := filepath.Clean(effortRoot)
	if gitRoot, ok := gitstate.MetadataRoot(effortRoot); ok {
		workspaceRoot = gitRoot
	}

	for _, rec := range records {
		if rec.RecordType != LedgerRecordAtomPlanned || rec.Scope == nil {
			continue
		}

		for _, rawPath := range append(append([]string{}, rec.Scope.ExpectedFiles...), rec.Scope.AllowedFiles...) {
			clean := filepath.ToSlash(strings.TrimSpace(rawPath))
			if clean == "" {
				continue
			}

			for _, candidate := range implementationHandoffExistingPathCandidates(clean, workflowState.Context.PathAliases) {
				if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(candidate))); err == nil {
					out[candidate] = true
				}
			}
		}
	}

	return out
}

func implementationHandoffExistingPathCandidates(path string, aliases map[string]string) []string {
	candidates := []string{path}
	if alias := strings.TrimSpace(aliases[path]); alias != "" {
		candidates = append(candidates, filepath.ToSlash(alias))
	}

	return candidates
}

func handoffAuthorizedNewPackageRoots(actions map[string]string) []string {
	var roots []string
	seen := map[string]bool{}

	for rawPath, rawAction := range actions {
		if !strings.Contains(strings.ToLower(strings.TrimSpace(rawAction)), "create new file") {
			continue
		}

		clean := strings.Trim(strings.TrimSpace(filepath.ToSlash(rawPath)), "/")
		if clean == "" {
			continue
		}

		root := filepath.ToSlash(filepath.Dir(clean))
		if root == "." || root == "/" || root == "" || seen[root] {
			continue
		}

		seen[root] = true
		roots = append(roots, root)
	}

	return briefUniqueSortedStrings(roots)
}

func coverageBlockerMessages(evaluation CoverageGateEvaluation) []string {
	messages := make([]string, 0, len(evaluation.Blockers))
	for _, blocker := range evaluation.Blockers {
		messages = append(messages, blocker.Message)
	}

	return briefUniqueSortedStrings(messages)
}

func coverageBlockerKinds(evaluation CoverageGateEvaluation) []string {
	kinds := make([]string, 0, len(evaluation.Blockers))
	for _, blocker := range evaluation.Blockers {
		if kind := strings.TrimSpace(blocker.Kind); kind != "" {
			kinds = append(kinds, kind)
		}
	}

	return briefUniqueSortedStrings(kinds)
}

func firstNonEmptyHandoffString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
