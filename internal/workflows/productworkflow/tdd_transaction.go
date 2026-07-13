package productworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

var ErrStaleTDDGeneration = errors.New("stale TDD generation")

// tddGenerationToken is intentionally opaque and is issued only while the
// product-workflow mutation lock is held. Its zero value is invalid, so an
// omitted token can never act as a wildcard.
type tddGenerationToken struct {
	generation workflowArtifactGeneration
}

type tddMutationLease struct {
	lease         *productWorkflowMutationLease
	currentBody   []byte
	workflowState WorkflowState
}

func inspectTDDGeneration(root *os.Root) (tddGenerationToken, []byte, WorkflowState, error) {
	workflowState, phase, err := inspectWorkflowAuthority(root)
	if err != nil {
		return tddGenerationToken{}, nil, WorkflowState{}, err
	}
	body, present, err := readWorkflowArtifactSnapshot(root, "tdd.json")
	if err != nil {
		return tddGenerationToken{}, nil, WorkflowState{}, err
	}
	if !present {
		return tddGenerationToken{generation: newWorkflowArtifactGeneration(
			workflowArtifactGenerationAbsent,
			"",
			workflowState,
			phase,
		)}, nil, workflowState, nil
	}
	sum := sha256.Sum256(body)

	return tddGenerationToken{
		generation: newWorkflowArtifactGeneration(
			workflowArtifactGenerationPresent,
			hex.EncodeToString(sum[:]),
			workflowState,
			phase,
		),
	}, body, workflowState, nil
}

func acquireTDDMutationLease(
	effortRoot string,
	expectedGeneration tddGenerationToken,
) (*tddMutationLease, error) {
	if err := expectedGeneration.validate(); err != nil {
		return nil, fmt.Errorf("validate expected TDD generation: %w", err)
	}
	lease, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		return nil, err
	}
	currentGeneration, currentBody, workflowState, err := inspectTDDGeneration(lease.root)
	if err != nil {
		lease.Close()
		return nil, fmt.Errorf("inspect current TDD generation: %w", err)
	}
	if !expectedGeneration.equal(currentGeneration) {
		lease.Close()
		return nil, fmt.Errorf(
			"%w: expected %s generation %q at workflow revision %d/%s/%s, found %s generation %q at workflow revision %d/%s/%s",
			ErrStaleTDDGeneration,
			expectedGeneration.generation.state,
			expectedGeneration.generation.digest,
			expectedGeneration.generation.stateRevision,
			expectedGeneration.generation.lastEventID,
			expectedGeneration.generation.phase,
			currentGeneration.generation.state,
			currentGeneration.generation.digest,
			currentGeneration.generation.stateRevision,
			currentGeneration.generation.lastEventID,
			currentGeneration.generation.phase,
		)
	}
	phase := currentGeneration.generation.phase
	if phase != PhaseTDDSynthesis {
		lease.Close()
		return nil, fmt.Errorf("TDD mutation requires workflow phase %q, got %q", PhaseTDDSynthesis, phase)
	}

	return &tddMutationLease{lease: lease, currentBody: currentBody, workflowState: workflowState}, nil
}

func (lease *tddMutationLease) Close() {
	if lease == nil {
		return
	}
	lease.lease.Close()
}

func (generation tddGenerationToken) validate() error {
	return generation.generation.validate("TDD")
}

func (generation tddGenerationToken) equal(other tddGenerationToken) bool {
	return generation.generation.equal(other.generation)
}

type committedArtifactError interface {
	error
	Committed() bool
}

func artifactWriteCommitted(err error) bool {
	if err == nil {
		return true
	}
	var committed committedArtifactError

	return errors.As(err, &committed) && committed.Committed()
}

func commitSynthesizedTDDLedger(
	effortRoot string,
	expectedGeneration tddGenerationToken,
	body []byte,
) error {
	lease, err := acquireTDDMutationLease(effortRoot, expectedGeneration)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := lease.lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("validate TDD synthesis effort root: %w", err)
	}

	return writeEffortArtifactAtomicRoot(lease.lease.root, "tdd.json", body, 0o644)
}

func validateTDDContractAuthority(
	state WorkflowState,
	ledger TDDLedger,
	contract BehavioralCoverageContract,
) (BehavioralCoverageContract, error) {
	if strings.TrimSpace(contract.EventID) != "" || strings.TrimSpace(contract.PromotionState) != "" {
		return BehavioralCoverageContract{}, errors.New("behavioral coverage candidate must not contain promotion provenance")
	}

	bound, err := bindTDDContractIdentity("effort_id", contract.EffortID, ledger.EffortID, state.EffortID)
	if err != nil {
		return BehavioralCoverageContract{}, err
	}
	contract.EffortID = bound
	bound, err = bindTDDContractIdentity("task_id", contract.TaskID, ledger.TaskID, state.TaskID)
	if err != nil {
		return BehavioralCoverageContract{}, err
	}
	contract.TaskID = bound
	bound, err = bindTDDContractIdentity("coverage_tier", contract.CoverageTier, ledger.CoverageTier, state.Coverage.CoverageTier)
	if err != nil {
		return BehavioralCoverageContract{}, err
	}
	contract.CoverageTier = bound
	if strings.TrimSpace(contract.ContractID) == "" {
		contract.ContractID = contract.TaskID
	}

	return contract, nil
}

func bindTDDContractIdentity(field, candidate, ledgerValue, workflowValue string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	ledgerValue = strings.TrimSpace(ledgerValue)
	workflowValue = strings.TrimSpace(workflowValue)
	if ledgerValue == "" || workflowValue == "" {
		return "", fmt.Errorf("behavioral coverage %s authority is missing", field)
	}
	if ledgerValue != workflowValue {
		return "", fmt.Errorf("tdd.json %s %q does not match workflow_state.json %s %q", field, ledgerValue, field, workflowValue)
	}
	if candidate != "" && candidate != ledgerValue {
		return "", fmt.Errorf("behavioral coverage %s %q does not match authoritative %s %q", field, candidate, field, ledgerValue)
	}

	return ledgerValue, nil
}
