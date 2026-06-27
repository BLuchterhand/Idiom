package planartifact

import (
	"encoding/json"
	"fmt"
)

func ParseJSON(body []byte) (PlanArtifact, error) {
	var artifact PlanArtifact

	if err := json.Unmarshal(body, &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("decode plan artifact: %w", err)
	}

	artifact = NormalizeLoadedPlanArtifact(artifact)
	if err := ValidatePlanArtifact(artifact); err != nil {
		return PlanArtifact{}, err
	}

	return artifact, nil
}
