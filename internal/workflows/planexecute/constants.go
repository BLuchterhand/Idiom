package planexecute

import (
	"errors"
	"time"

	lanecontract "idiom/internal/contracts/lane"
)

const planExecuteLaneName = string(lanecontract.LanePlanExecute)

const defaultPlanExecuteVerificationTimeout = 10 * time.Minute

var ErrPlanArtifactNotFound = errors.New("plan artifact not found")
