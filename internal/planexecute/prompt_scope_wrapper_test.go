package planexecute

import (
	"slices"
	"testing"

	"idiom/internal/plancontract"
)

func TestPromptScopeWrappersMatchPlanContract(t *testing.T) {
	prompt := `
## Files To Modify

- pkg/required.go

## Allowed Edit Paths

- pkg/allowed.go
`
	if got, want := RequiredEditPathsFromPrompt(prompt), plancontract.RequiredEditPathsFromPrompt(prompt); !slices.Equal(got, want) {
		t.Fatalf("required wrapper = %#v, want %#v", got, want)
	}
	if got, want := AllowedEditPathsFromPrompt(prompt), plancontract.AllowedEditPathsFromPrompt(prompt); !slices.Equal(got, want) {
		t.Fatalf("allowed wrapper = %#v, want %#v", got, want)
	}
}
