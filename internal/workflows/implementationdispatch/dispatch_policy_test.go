package implementationdispatch

import (
	"slices"
	"strings"
	"testing"
)

func TestPlanDocumentsNeedRefineBeforeDispatchDetectsAtomizedDocs(t *testing.T) {
	docs := []PlanDocument{
		{Path: "docs/design.md"},
		{Path: ".idiom/atoms/design/atom-001.md"},
	}

	if !PlanDocumentsNeedRefineBeforeDispatch(docs) {
		t.Fatalf("atomized plan document should require refine before dispatch")
	}

	if PlanDocumentsNeedRefineBeforeDispatch([]PlanDocument{{Path: "docs/design.md"}}) {
		t.Fatalf("plain design document should not require atom refine")
	}
}

func TestTestObligationsFromDocumentsDeduplicates(t *testing.T) {
	got := TestObligationsFromDocuments([]PlanDocument{
		{TestObligations: []string{"go test ./pkg", "go test ./pkg"}},
		{TestObligations: []string{"go test ./other"}},
	})

	want := []string{"go test ./pkg", "go test ./other"}
	if !slices.Equal(got, want) {
		t.Fatalf("obligations = %#v, want %#v", got, want)
	}
}

func TestAuthorizedNewFilesCarriesTypedPromptAuthority(t *testing.T) {
	prompt := strings.Join([]string{
		"Implement atom.",
		"Typed plan execution contract:",
		`{"authorized_new_files":[{"path":"pkg/new_helper.go"},{"path":"pkg/new_helper_test.go"}]}`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-1"}`,
	}, "\n")

	want := []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	if got := AuthorizedNewFiles(prompt, nil); !slices.Equal(got, want) {
		t.Fatalf("authorized new files = %#v, want %#v", got, want)
	}
}

func TestAuthorizedNewFilesCarriesPlanDocumentTypedAuthority(t *testing.T) {
	doc := PlanDocument{Content: strings.Join([]string{
		"Plan document.",
		"Typed plan execution contract:",
		`{"authorized_new_files":[{"path":"pkg/from_doc.go"}]}`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-1"}`,
	}, "\n")}

	if got := AuthorizedNewFiles("Implement atom.", []PlanDocument{doc}); !slices.Equal(got, []string{"pkg/from_doc.go"}) {
		t.Fatalf("authorized new files = %#v, want plan document authority", got)
	}
}

func TestAuthorizedNewFilesIgnoresPromptProseWithoutTypedContract(t *testing.T) {
	prompt := strings.Join([]string{
		"Create new file pkg/new_helper.go with the following content.",
		"Typed plan execution contract:",
		`{"authorized_new_files":`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-1"}`,
	}, "\n")

	if got := AuthorizedNewFiles(prompt, nil); len(got) != 0 {
		t.Fatalf("prompt prose or malformed typed contract must not authorize new files: %#v", got)
	}
}

func TestPlanExecuteMaxTokensMatchesCLIPlannerDefault(t *testing.T) {
	got := PlanExecuteMaxTokens(
		"reject temp symlinks and add regression tests proving no partial mutation",
		nil,
		[]string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"},
		nil,
	)
	if got != 8192 {
		t.Fatalf("bounded existing-file plan max tokens = %d, want tighter existing-file budget 8192", got)
	}
}

func TestPlanExecuteMaxTokensDoesNotDowngradeLargeNewFilePlans(t *testing.T) {
	got := PlanExecuteMaxTokens(
		"create new file with the full file body",
		[]string{"internal/workflows/productworkflow/new_feature.go"},
		nil,
		nil,
	)
	if got != 16384 {
		t.Fatalf("new-file plan max tokens = %d, want CLI default 16384", got)
	}
}

func TestPlanExecuteMaxTokensDoesNotUsePromptTestHeuristic(t *testing.T) {
	got := PlanExecuteMaxTokens(
		"add regression tests proving the parser rejects invalid routes",
		[]string{"internal/parser/route_test.go"},
		nil,
		nil,
	)

	if got != 8192 {
		t.Fatalf("prompt prose must not inflate missing-test budget, got %d want 8192", got)
	}
}

func TestPlanExecuteMaxTokensUsesLargeBudgetForPlanDocNewFile(t *testing.T) {
	got := PlanExecuteMaxTokens(
		"implement the referenced plan",
		[]string{"internal/contracts/writerroute/decision.go"},
		nil,
		[]PlanDocument{{
			Content:   "## Files To Modify\n\n- internal/contracts/writerroute/decision.go - NEW FILE\n\nUse replace_all:true with the full file body.",
			EditPaths: []string{"internal/contracts/writerroute/decision.go"},
		}},
	)
	if got != 16384 {
		t.Fatalf("plan-doc new-file budget = %d, want 16384", got)
	}
}

func TestPlanExecuteMaxTokensDoesNotUsePlanDocTestHeuristic(t *testing.T) {
	got := PlanExecuteMaxTokens(
		"implement docs/parser-plan.md",
		nil,
		nil,
		[]PlanDocument{{
			Content:   "## Files To Modify\n\n- tests/parser_test.py\n\nAdd a regression test for invalid parser routes.",
			EditPaths: []string{"tests/parser_test.py"},
		}},
	)

	if got != 8192 {
		t.Fatalf("plan-doc prose must not inflate missing-test budget, got %d want 8192", got)
	}
}
