package productworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

type workflowArtifactGenerationState string

const (
	workflowArtifactGenerationAbsent  workflowArtifactGenerationState = "absent"
	workflowArtifactGenerationPresent workflowArtifactGenerationState = "present"
)

// workflowArtifactGeneration binds an artifact digest to the workflow
// authority under which it was observed. Revision, event identity, and phase
// prevent an unchanged artifact from surviving a workflow phase ABA.
type workflowArtifactGeneration struct {
	state         workflowArtifactGenerationState
	digest        string
	stateRevision int
	lastEventID   string
	phase         CreationPhase
}

func inspectWorkflowAuthority(root *os.Root) (WorkflowState, CreationPhase, error) {
	if root == nil {
		return WorkflowState{}, "", errors.New("product workflow root is not open")
	}
	body, present, err := readWorkflowArtifactSnapshot(root, WorkflowStateFileName)
	if err != nil {
		return WorkflowState{}, "", fmt.Errorf("read %s: %w", WorkflowStateFileName, err)
	}
	if !present {
		return WorkflowState{}, "", fmt.Errorf("read %s: %w", WorkflowStateFileName, os.ErrNotExist)
	}
	state, err := ParseWorkflowStateJSON(body)
	if err != nil {
		return WorkflowState{}, "", fmt.Errorf("parse %s: %w", WorkflowStateFileName, err)
	}
	phase, err := validateCreationWorkflowAuthority(state)
	if err != nil {
		return WorkflowState{}, "", err
	}

	return state, phase, nil
}

func readWorkflowArtifactSnapshot(root *os.Root, path string) ([]byte, bool, error) {
	body, err := readEffortArtifactRoot(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	return body, true, nil
}

func newWorkflowArtifactGeneration(
	state workflowArtifactGenerationState,
	digest string,
	workflowState WorkflowState,
	phase CreationPhase,
) workflowArtifactGeneration {
	return workflowArtifactGeneration{
		state:         state,
		digest:        digest,
		stateRevision: workflowState.StateRevision,
		lastEventID:   strings.TrimSpace(workflowState.LastEventID),
		phase:         phase,
	}
}

func (generation workflowArtifactGeneration) validate(artifact string) error {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		artifact = "workflow artifact"
	}
	switch generation.state {
	case workflowArtifactGenerationAbsent:
		if generation.digest != "" {
			return fmt.Errorf("absent %s generation must not carry a digest", artifact)
		}
	case workflowArtifactGenerationPresent:
		if !validWorkflowArtifactDigest(generation.digest) {
			return fmt.Errorf("present %s generation has an invalid digest", artifact)
		}
	default:
		return fmt.Errorf("%s generation state is required", artifact)
	}
	if generation.stateRevision < 0 {
		return fmt.Errorf("%s generation has an invalid workflow state revision", artifact)
	}
	if generation.lastEventID == "" {
		return fmt.Errorf("%s generation is missing workflow event identity", artifact)
	}
	if _, ok := creationPhaseProjections[generation.phase]; !ok {
		return fmt.Errorf("%s generation has an invalid workflow phase %q", artifact, generation.phase)
	}

	return nil
}

func (generation workflowArtifactGeneration) equal(other workflowArtifactGeneration) bool {
	return generation.state == other.state &&
		generation.digest == other.digest &&
		generation.stateRevision == other.stateRevision &&
		generation.lastEventID == other.lastEventID &&
		generation.phase == other.phase
}

func validWorkflowArtifactDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)

	return err == nil
}
