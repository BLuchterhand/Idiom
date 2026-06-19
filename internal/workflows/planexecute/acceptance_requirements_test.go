package planexecute

import (
	"encoding/json"
	"slices"
	"testing"

	"idiom/internal/contracts/plan"
)

func TestExtractRequiredAcceptanceEvidenceIDsFromTypedAtomContract(t *testing.T) {
	body, err := json.Marshal(plancontract.AtomExecutionContract{
		AcceptanceObligations: []plancontract.AcceptanceEvidenceRequired{
			{ID: "coverage-threshold", Required: true},
			{ID: "optional-note", Required: false},
			{ID: "review-evidence", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("marshal atom contract: %v", err)
	}

	prompt := "brief\n\n" + planExecuteTypedAtomContractMarker + "\n" + string(body)
	got := ExtractRequiredAcceptanceEvidenceIDs(prompt)
	want := []string{"coverage-threshold", "optional-note", "review-evidence"}

	if !slices.Equal(got, want) {
		t.Fatalf("required acceptance evidence IDs = %#v, want %#v", got, want)
	}
}
