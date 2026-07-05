package plan

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestRequiredAcceptanceEvidenceIDsFromTypedAtomContract(t *testing.T) {
	body, err := json.Marshal(AtomExecutionContract{
		AcceptanceObligations: []AcceptanceEvidenceRequired{
			{ID: "coverage-threshold", Required: true},
			{ID: "optional-note", Required: false},
			{ID: "review-evidence", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("marshal atom contract: %v", err)
	}

	prompt := "brief\n\n" + PromptAtomExecutionContractMarker + "\n" + string(body)
	got := RequiredAcceptanceEvidenceIDsFromPrompt(prompt)
	want := []string{"coverage-threshold", "optional-note", "review-evidence"}

	if !slices.Equal(got, want) {
		t.Fatalf("required acceptance evidence IDs = %#v, want %#v", got, want)
	}
}

func TestRequiredAcceptanceEvidenceIDsFromTypedPlanContractBeforeAuthorityProjection(t *testing.T) {
	body, err := json.Marshal(PlanExecutionContract{
		Atoms: []AtomExecutionContract{{
			AcceptanceObligations: []AcceptanceEvidenceRequired{{
				ID:       "plan-evidence",
				Required: true,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal plan contract: %v", err)
	}

	prompt := "brief\n\n" + PromptPlanExecutionContractMarker + "\n" + string(body) + "\n" +
		PromptAtomExecutionContractMarker + "\nnot json"
	got := RequiredAcceptanceEvidenceIDsFromPrompt(prompt)
	want := []string{"plan-evidence"}

	if !slices.Equal(got, want) {
		t.Fatalf("required acceptance evidence IDs = %#v, want %#v", got, want)
	}
}

func TestAtomExecutionContractFromPromptHandlesNestedJSONAndBracedStrings(t *testing.T) {
	prompt := `brief

Typed plan authority projection:
{
  "atom_id": "atom-braces",
  "scope": {
    "required_coverage_paths": ["pkg/feature.go"]
  },
  "acceptance_obligations": [
    {
      "id": "braced-string",
      "description": "keep a literal { brace } inside this string",
      "required": true
    }
  ]
}
`

	atom, ok := AtomExecutionContractFromPrompt(prompt)
	if !ok {
		t.Fatal("expected atom contract to parse")
	}
	if atom.AtomID != "atom-braces" {
		t.Fatalf("atom id = %q, want atom-braces", atom.AtomID)
	}

	got := RequiredAcceptanceEvidenceIDsFromPrompt(prompt)
	want := []string{"braced-string"}

	if !slices.Equal(got, want) {
		t.Fatalf("required acceptance evidence IDs = %#v, want %#v", got, want)
	}
}

func TestRequiredAcceptanceEvidenceIDsFromPromptPrefersAtomContractOverPlanContract(t *testing.T) {
	planBody, err := json.Marshal(PlanExecutionContract{
		Atoms: []AtomExecutionContract{{
			AcceptanceObligations: []AcceptanceEvidenceRequired{{
				ID:       "plan-only",
				Required: true,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal plan contract: %v", err)
	}

	atomBody, err := json.Marshal(AtomExecutionContract{
		AcceptanceObligations: []AcceptanceEvidenceRequired{{
			ID:       "atom-wins",
			Required: true,
		}},
	})
	if err != nil {
		t.Fatalf("marshal atom contract: %v", err)
	}

	prompt := PromptPlanExecutionContractMarker + "\n" + string(planBody) + "\n\n" +
		PromptAtomExecutionContractMarker + "\n" + string(atomBody)
	got := RequiredAcceptanceEvidenceIDsFromPrompt(prompt)
	want := []string{"atom-wins"}

	if !slices.Equal(got, want) {
		t.Fatalf("required acceptance evidence IDs = %#v, want %#v", got, want)
	}
}

func TestRequiredAcceptanceEvidenceIDsFromPromptIgnoresMalformedOrMissingJSON(t *testing.T) {
	for _, prompt := range []string{
		"brief without a typed contract",
		PromptAtomExecutionContractMarker + "\nnot json",
		PromptPlanExecutionContractMarker + "\n{\"atoms\":[",
	} {
		t.Run(prompt, func(t *testing.T) {
			if got := RequiredAcceptanceEvidenceIDsFromPrompt(prompt); len(got) != 0 {
				t.Fatalf("required acceptance evidence IDs = %#v, want none", got)
			}
		})
	}
}

func TestAuthorizedNewFilePathsFromPromptStopsBeforeAtomProjection(t *testing.T) {
	body, err := json.Marshal(PlanExecutionContract{
		AuthorizedNewFiles: []AuthorizedNewFile{{
			Path: "pkg/new_helper.go",
		}},
	})
	if err != nil {
		t.Fatalf("marshal plan contract: %v", err)
	}

	prompt := "brief\n\n" + PromptPlanExecutionContractMarker + "\n" + string(body) + "\n" +
		PromptAtomExecutionContractMarker + "\nnot json"

	if got := AuthorizedNewFilePathsFromPrompt(prompt); !slices.Equal(got, []string{"pkg/new_helper.go"}) {
		t.Fatalf("authorized new files = %#v", got)
	}
}

func TestAuthorizedNewFilePathsFromPromptNormalizesAndDedupes(t *testing.T) {
	body, err := json.Marshal(PlanExecutionContract{
		AuthorizedNewFiles: []AuthorizedNewFile{{
			Path: "pkg/b.go",
		}, {
			Path: " ./pkg/a.go ",
		}, {
			Path: "pkg/b.go",
		}, {
			Path: "../outside.go",
		}},
	})
	if err != nil {
		t.Fatalf("marshal plan contract: %v", err)
	}

	prompt := PromptPlanExecutionContractMarker + "\n" + string(body)
	if got := AuthorizedNewFilePathsFromPrompt(prompt); !slices.Equal(got, []string{"pkg/a.go", "pkg/b.go"}) {
		t.Fatalf("authorized new files = %#v", got)
	}
}

func TestAuthorizedNewFilePathsFromPromptIgnoresMalformedOrMissingJSON(t *testing.T) {
	for _, prompt := range []string{
		"brief without typed plan contract",
		PromptPlanExecutionContractMarker + "\nnot json",
		PromptPlanExecutionContractMarker + "\n{\"authorized_new_files\":[",
	} {
		t.Run(prompt, func(t *testing.T) {
			if got := AuthorizedNewFilePathsFromPrompt(prompt); len(got) != 0 {
				t.Fatalf("authorized new files = %#v, want none", got)
			}
		})
	}
}
