package plan

import (
	"slices"
	"testing"
)

func TestEditSetObligationsNormalizeAndMergeByKind(t *testing.T) {
	obligations := MergeEditSetObligations(
		EditSetObligations{
			{Kind: EditSetObligationKindImplementationEdit, Path: " ./pkg/feature.go ", Source: "impl"},
			{Kind: EditSetObligationKindTestEdit, Path: "pkg/feature_test.go", Source: "test"},
			{Kind: EditSetObligationKindTestEdit, Path: "pkg\\feature_test.go", Source: "later"},
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

	if obligations[0].Path != "" || obligations[1].Path != "pkg/feature_test.go" || obligations[2].Path != "" || obligations[3].Path != "pkg/feature.go" {
		t.Fatalf("obligations were not stably sorted by kind and normalized path: %#v", obligations)
	}

	if obligations[0].Source != "scope_contract" || obligations[1].Source != "test" || obligations[2].Source != "options" || obligations[3].Source != "impl" {
		t.Fatalf("obligation sources = %#v, want first source preserved for each kind/path", obligations)
	}
}

func TestEditSetObligationRejectionReasonClassifiesChangedPaths(t *testing.T) {
	obligations := EditSetObligations{}.
		With(EditSetObligationKindTestEdit, "test").
		With(EditSetObligationKindImplementationEdit, "test")

	if got := obligations.RejectionReason([]string{"pkg/feature.go", "pkg/feature_test.go"}); got != "" {
		t.Fatalf("source and test edits should satisfy obligations, got %q", got)
	}

	if got := obligations.RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); got != PlanExecuteFailureMissingRequestedImplementationEdit {
		t.Fatalf("docs plus tests must not satisfy implementation obligation, got %q", got)
	}

	if got := obligations.MissingOpenObligations([]string{"pkg/feature.go"}); !slices.Equal(got, []string{"requested_test_edit_missing"}) {
		t.Fatalf("missing obligations = %#v, want test edit missing", got)
	}
}

func TestEditSetObligationsReportNormalizedOpenExactPaths(t *testing.T) {
	obligations := EditSetObligationsForRequiredPaths(
		[]string{"pkg/feature.go", "pkg/feature_test.go", "docs/feature.md", "pkg/feature_test.go"},
		"required_scope",
	)

	open := obligations.Open([]string{"pkg/feature.go"})
	if len(open) != 1 {
		t.Fatalf("open obligations = %#v, want one missing test path", open)
	}

	if got := open[0]; got.Code != EditSetOpenObligationRequestedTestEditMissing || got.Kind != EditSetObligationKindTestEdit || got.Path != "pkg/feature_test.go" || got.Source != "required_scope" {
		t.Fatalf("open obligation = %#v, want normalized missing test path", got)
	}
}

func TestRequiredPathObligationsClassifyTestsAndImplementation(t *testing.T) {
	obligations := EditSetObligationsForRequiredPaths(
		[]string{
			"pkg/feature.go",
			"pkg/feature_test.go",
			"web/component.spec.tsx",
			"prompts/agent.md",
			"README.md",
		},
		"required_scope",
	)

	var got []EditSetObligation
	for _, obligation := range obligations {
		got = append(got, EditSetObligation{Kind: obligation.Kind, Path: obligation.Path})
	}

	want := []EditSetObligation{
		{Kind: EditSetObligationKindTestEdit, Path: "pkg/feature_test.go"},
		{Kind: EditSetObligationKindTestEdit, Path: "web/component.spec.tsx"},
		{Kind: EditSetObligationKindImplementationEdit, Path: "pkg/feature.go"},
		{Kind: EditSetObligationKindImplementationEdit, Path: "prompts/agent.md"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("required path obligations = %#v, want %#v", got, want)
	}
}
