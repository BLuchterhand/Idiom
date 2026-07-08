package workspace

import (
	"slices"
	"testing"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/semantic/languagetools"
)

func TestLanguageToolResultPreservesFacts(t *testing.T) {
	paths := []string{"internal/app.go"}
	result := fromLanguageToolResult(languagetools.ToolResult{
		Tool:  "go_diagnostics",
		Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: paths},
	})
	paths[0] = "mutated.go"

	if result.Facts == nil || !slices.Equal(result.Facts.DiagnosticEvidencePaths, []string{"internal/app.go"}) {
		t.Fatalf("fromLanguageToolResult facts = %#v", result.Facts)
	}

	result.Facts.DiagnosticEvidencePaths[0] = "internal/roundtrip.go"
	roundTrip := toLanguageToolResult(result)
	result.Facts.DiagnosticEvidencePaths[0] = "mutated-again.go"

	if roundTrip.Facts == nil || !slices.Equal(roundTrip.Facts.DiagnosticEvidencePaths, []string{"internal/roundtrip.go"}) {
		t.Fatalf("toLanguageToolResult facts = %#v", roundTrip.Facts)
	}
}
