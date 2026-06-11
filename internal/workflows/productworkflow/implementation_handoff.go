package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/util"
)

type ImplementationHandoff struct {
	EffortID             string
	TaskID               string
	EffortRoot           string
	ArtifactRootPath     string
	StatusBucket         string
	CurrentPhase         string
	CurrentState         string
	NextValidActions     []string
	CoverageTier         string
	CoverageGatePassed   bool
	CoverageBlockers     []string
	CoverageBlockerKinds []string
	PRDRequirements      []ImplementationHandoffPRDRequirement
	AcceptedADRIDs       []string
	AcceptedADRPaths     []string
	TDDObligations       []ImplementationHandoffTDDObligation
	FilesToModify        []string
	ContextEvidencePaths []string
	RequiredBehavior     []string
	Tests                []string
	Acceptance           []string
	Constraints          []string
	Decisions            []string
	Blockers             []string
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

func LoadImplementationHandoff(effortRoot string) (ImplementationHandoff, error) {
	root := filepath.Clean(effortRoot)

	stateData, err := os.ReadFile(filepath.Join(root, WorkflowStateFileName))
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("load workflow state: %w", err)
	}
	workflowState, err := ParseWorkflowStateJSON(stateData)
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("parse workflow state: %w", err)
	}

	prdData, err := os.ReadFile(filepath.Join(root, "prd.md"))
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("load prd.md: %w", err)
	}
	prd, err := ParsePRDMarkdown(prdData)
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("parse prd.md: %w", err)
	}

	tddData, err := os.ReadFile(filepath.Join(root, "tdd.json"))
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("load tdd.json: %w", err)
	}
	tdd, err := ParseTDDLedgerJSON(tddData)
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("parse tdd.json: %w", err)
	}

	adrIndex, parsedADRs, err := loadImplementationHandoffADRs(root)
	if err != nil {
		return ImplementationHandoff{}, err
	}
	traceability := loadImplementationHandoffTraceability(root)
	implementationRecords := loadImplementationHandoffLedger(root)

	satisfiedTDDIDs := BuildSatisfiedTDDObligationIDs(tdd, implementationRecords)
	coverage := EvaluateCoverageGate(prd, adrIndex, parsedADRs, tdd, traceability, satisfiedTDDIDs)

	return BuildImplementationHandoff(BriefGenerationInput{
		EffortRoot:            root,
		ArtifactRootPath:      root,
		WorkflowState:         workflowState,
		PRD:                   prd,
		ADRIndex:              adrIndex,
		ADRs:                  parsedADRs,
		TDD:                   tdd,
		Traceability:          traceability,
		ImplementationRecords: implementationRecords,
		CoverageEvaluation:    coverage,
	}), nil
}

func BuildImplementationHandoff(input BriefGenerationInput) ImplementationHandoff {
	generated := GenerateImplementationBrief(input)
	coverageTier := strings.TrimSpace(input.CoverageEvaluation.CoverageTier)
	if coverageTier == "" {
		coverageTier = strings.TrimSpace(input.TDD.CoverageTier)
	}
	handoff := ImplementationHandoff{
		EffortID:             firstNonEmptyHandoffString(input.WorkflowState.EffortID, input.TDD.EffortID, input.CoverageEvaluation.EffortID),
		TaskID:               firstNonEmptyHandoffString(input.WorkflowState.TaskID, input.TDD.TaskID, input.PRD.FrontMatter.TaskID, input.CoverageEvaluation.TaskID),
		EffortRoot:           strings.TrimSpace(input.EffortRoot),
		ArtifactRootPath:     firstNonEmptyHandoffString(input.ArtifactRootPath, input.EffortRoot),
		StatusBucket:         strings.TrimSpace(input.WorkflowState.StatusBucket),
		CurrentPhase:         strings.TrimSpace(input.WorkflowState.CurrentPhase),
		CurrentState:         strings.TrimSpace(input.WorkflowState.CurrentState),
		NextValidActions:     append([]string{}, input.WorkflowState.NextValidActions...),
		CoverageTier:         coverageTier,
		CoverageGatePassed:   util.EqualTrimFold(input.CoverageEvaluation.Status, ProductWorkflowStatusPass),
		CoverageBlockers:     coverageBlockerMessages(input.CoverageEvaluation),
		CoverageBlockerKinds: coverageBlockerKinds(input.CoverageEvaluation),
		FilesToModify:        append([]string{}, generated.FilesToModify...),
		ContextEvidencePaths: briefUniqueSortedStrings(input.WorkflowState.Context.ReferencedPaths),
		RequiredBehavior:     append([]string{}, generated.RequiredBehavior...),
		Tests:                append([]string{}, generated.Tests...),
		Acceptance:           append([]string{}, generated.Acceptance...),
		Constraints:          append([]string{}, generated.Constraints...),
		Decisions:            append([]string{}, generated.Decisions...),
		Blockers:             append([]string{}, generated.Blockers...),
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

func loadImplementationHandoffADRs(root string) (ADRIndex, []ParsedADR, error) {
	adrIndexData, err := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
	if err != nil {
		return ADRIndex{}, nil, nil
	}
	adrIndex, err := ParseADRIndexJSON(adrIndexData)
	if err != nil {
		return ADRIndex{}, nil, fmt.Errorf("parse adrs/index.json: %w", err)
	}
	var parsedADRs []ParsedADR
	for _, entry := range adrIndex.ADRs {
		if !isAcceptedArtifact(entry.Status) || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		adrData, adrErr := os.ReadFile(filepath.Join(root, "adrs", entry.Path))
		if adrErr != nil {
			continue
		}
		adrDoc, adrErr := ParseADRMarkdown(adrData)
		if adrErr != nil {
			continue
		}
		parsedADRs = append(parsedADRs, ParsedADR{
			Doc:         adrDoc,
			RelPath:     filepath.ToSlash(filepath.Join("adrs", entry.Path)),
			RawMarkdown: string(adrData),
		})
	}
	return adrIndex, parsedADRs, nil
}

func loadImplementationHandoffTraceability(root string) TraceabilityMatrix {
	matrixData, err := os.ReadFile(filepath.Join(root, "evidence", "traceability_matrix.json"))
	if err != nil {
		return TraceabilityMatrix{}
	}
	traceability, err := ParseTraceabilityMatrixJSON(matrixData)
	if err != nil {
		return TraceabilityMatrix{}
	}
	return traceability
}

func loadImplementationHandoffLedger(root string) []ImplementationLedgerRecord {
	data, err := os.ReadFile(filepath.Join(root, "implementation_ledger.jsonl"))
	if err != nil {
		return nil
	}
	records, err := ParseImplementationLedgerJSONL(data)
	if err != nil {
		return nil
	}
	return records
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
