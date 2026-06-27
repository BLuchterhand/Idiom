package contractcompile

import "testing"

func TestTurnPhaseVocabularyIncludesCompileDefault(t *testing.T) {
	if !KnownTurnPhaseID(TurnPhaseRequiredSource) {
		t.Fatalf("KnownTurnPhaseID(%q) = false", TurnPhaseRequiredSource)
	}
	if KnownTurnPhaseID(TurnPhaseID("plan_execute.surprise")) {
		t.Fatal("KnownTurnPhaseID accepted an undeclared plan-execute phase")
	}
}
