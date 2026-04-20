package frontier

import (
	"slices"
	"strings"
	"testing"

	"qwen-bubbles/internal/workspace"
)

func TestLocatePolicySourceOfTruth(t *testing.T) {
	candidates := ParseLocateCandidates(strings.Join([]string{
		"f1 internal/app/app.go:42:header := titleStyle.Render(\"Qwen\")",
		"f2 internal/app/app_test.go:12:scoreLocateCandidate should not be a target",
	}, "\n"))
	if len(candidates) != 1 {
		t.Fatalf("expected one non-meta locate candidate, got %#v", candidates)
	}
	candidates[0].Snippet = "header += titleStyle.Render(\"Qwen is working\")"
	candidates[0].Region = ClassifyLocateRegion(candidates[0])
	candidates[0].Score = ScoreLocateCandidate("remove the top window banner", candidates[0])
	if candidates[0].Region != "header" || candidates[0].Score <= 0 {
		t.Fatalf("expected header candidate to score positively, got %#v", candidates[0])
	}
}

func TestExploreWorkflowAndWritePoliciesComeFromFrontier(t *testing.T) {
	terms := ExploreQueryTerms("recommend maintainability cleanup around renderer state")
	if slices.Contains(terms, "recommend") || slices.Contains(terms, "maintainability") {
		t.Fatalf("expected recommendation stopwords to be filtered, got %#v", terms)
	}
	if ArchitectureSearchPattern("find architectural maintainability boundaries") == "" {
		t.Fatalf("expected maintainability architecture prompt to produce a search pattern")
	}
	if ExploreBootstrapReadBudget(workspace.ThoroughnessQuick) >= ExploreBootstrapReadBudget(workspace.ThoroughnessThorough) {
		t.Fatalf("expected thorough exploration to have a larger bootstrap read budget")
	}
	if !LooksLikeWorkflowApprovalPrompt("build it") {
		t.Fatalf("expected terse build approval to be detected")
	}
	if !LooksLikeWorkflowApprovalPrompt("Begin the plan, but preserve the guardrails.") {
		t.Fatalf("expected explicit begin-the-plan approval to be detected")
	}
	if LooksLikeWorkflowApprovalPrompt("do not build it yet") {
		t.Fatalf("negative approval boundary should not be treated as approval")
	}
	if !LooksLikeConversationMemoryNotePrompt("remember that any later docs write must touch readme.md only") {
		t.Fatalf("expected memory-note prompt to be detected")
	}
	if !ExplicitMultiFileRequested("do a multi-file refactor across files") {
		t.Fatalf("expected explicit multi-file guarded-write request")
	}
}
