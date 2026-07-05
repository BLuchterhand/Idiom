package workerruntime

import "testing"

func TestConstraintVocabulary(t *testing.T) {
	for _, id := range []ConstraintID{
		ConstraintPlanThenExecuteSubmitPlanReady,
		ConstraintRequiredEditPathsDefineWriteScope,
		ConstraintExactAnchorImplementation,
		ConstraintImplementationPacketWriteReady,
		ConstraintAnswerReadinessReached,
		ConstraintChildPacketAvailable,
	} {
		if !ConstraintPresent([]string{" " + string(id) + " "}, id) {
			t.Fatalf("ConstraintPresent did not find %q", id)
		}
	}
}
