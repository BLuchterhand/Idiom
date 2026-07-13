package productworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// ErrStalePRDGeneration reports that a PRD write was prepared from a document
// or workflow generation that is no longer current.
var ErrStalePRDGeneration = errors.New("stale PRD generation")

// PRDGenerationToken identifies the exact PRD and workflow authority observed
// by LoadPRDDraftContext. It is intentionally opaque and its zero value is
// invalid, so callers can carry authority but cannot manufacture it.
type PRDGenerationToken struct {
	generation workflowArtifactGeneration
}

func (generation PRDGenerationToken) validate() error {
	return generation.generation.validate("PRD")
}

func (generation PRDGenerationToken) equal(other PRDGenerationToken) bool {
	return generation.generation.equal(other.generation)
}

func inspectPRDGeneration(root *os.Root) (PRDGenerationToken, []byte, WorkflowState, error) {
	workflowState, phase, err := inspectWorkflowAuthority(root)
	if err != nil {
		return PRDGenerationToken{}, nil, WorkflowState{}, err
	}

	body, present, err := readWorkflowArtifactSnapshot(root, "prd.md")
	if err != nil {
		return PRDGenerationToken{}, nil, WorkflowState{}, fmt.Errorf("read prd.md: %w", err)
	}
	if !present {
		return PRDGenerationToken{generation: newWorkflowArtifactGeneration(
			workflowArtifactGenerationAbsent,
			"",
			workflowState,
			phase,
		)}, nil, workflowState, nil
	}
	sum := sha256.Sum256(body)
	return PRDGenerationToken{generation: newWorkflowArtifactGeneration(
		workflowArtifactGenerationPresent,
		hex.EncodeToString(sum[:]),
		workflowState,
		phase,
	)}, body, workflowState, nil
}

func stalePRDGenerationError(expected, current PRDGenerationToken) error {
	return fmt.Errorf(
		"%w: expected %s generation %q at workflow revision %d/%s/%s, found %s generation %q at workflow revision %d/%s/%s",
		ErrStalePRDGeneration,
		expected.generation.state,
		expected.generation.digest,
		expected.generation.stateRevision,
		expected.generation.lastEventID,
		expected.generation.phase,
		current.generation.state,
		current.generation.digest,
		current.generation.stateRevision,
		current.generation.lastEventID,
		current.generation.phase,
	)
}
