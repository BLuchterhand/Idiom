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
		if !KnownConstraintID(id) {
			t.Fatalf("KnownConstraintID(%q) = false", id)
		}
		if !ConstraintPresent([]string{" " + string(id) + " "}, id) {
			t.Fatalf("ConstraintPresent did not find %q", id)
		}
	}

	if KnownConstraintID(ConstraintID("plan_then_execute_submit_ready")) {
		t.Fatal("KnownConstraintID accepted an undeclared constraint")
	}
}
