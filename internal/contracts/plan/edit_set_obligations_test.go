package plancontract

import (
	"slices"
	"testing"
)

func TestEditSetObligationsNormalizeAndMergeByKind(t *testing.T) {
	obligations := MergeEditSetObligations(
		EditSetObligations{
			{Kind: EditSetObligationKindTestEdit},
			{Kind: "invalid"},
		}.WithDefaultSource("scope_contract"),
		EditSetObligations{
			{Kind: EditSetObligationKindImplementationEdit},
			{Kind: EditSetObligationKindTestEdit, Source: "later"},
		}.WithDefaultSource("options"),
	)

	if got, want := obligations.Kinds(), []string{"test_edit_required", "implementation_edit_required"}; !slices.Equal(got, want) {
		t.Fatalf("obligation kinds = %#v, want %#v", got, want)
	}
	if obligations[0].Source != "scope_contract" || obligations[1].Source != "options" {
		t.Fatalf("obligation sources = %#v, want scope_contract/options", obligations)
	}
}

func TestEditSetObligationRejectionReasonClassifiesChangedPaths(t *testing.T) {
	obligations := EditSetObligations{}.
		With(EditSetObligationKindTestEdit, "test").
		With(EditSetObligationKindImplementationEdit, "test")

	if got := obligations.RejectionReason([]string{"pkg/feature.go", "pkg/feature_test.go"}); got != "" {
		t.Fatalf("source and test edits should satisfy obligations, got %q", got)
	}
	if got := obligations.RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); got != EditSetFailureMissingRequestedImplementationEdit {
		t.Fatalf("docs plus tests must not satisfy implementation obligation, got %q", got)
	}
	if got := obligations.MissingOpenObligations([]string{"pkg/feature.go"}); !slices.Equal(got, []string{"requested_test_edit_missing"}) {
		t.Fatalf("missing obligations = %#v, want test edit missing", got)
	}
}
