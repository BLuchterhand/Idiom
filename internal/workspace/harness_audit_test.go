package workspace

import (
	"strings"
	"testing"

	"idiom/internal/comprehension"
)

func TestBuildRankerLearningAuditEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	got := buildRankerLearningAudit(root)
	if got == nil {
		t.Fatal("expected report")
	}
	if got.WeightsFileExists || got.LearningLogExists {
		t.Fatalf("fresh workspace should have no ranker artifacts: %#v", got)
	}
	if len(got.WiredSources) != 0 {
		t.Fatalf("no sources should be wired yet, got %v", got.WiredSources)
	}
	if len(got.DormantSources) != len(rankerLearningKnownTriggers) {
		t.Fatalf("all known triggers should be dormant; got %v", got.DormantSources)
	}
	for _, t2 := range got.TriggerRecommendations {
		if t2.Status != "dormant" {
			t.Fatalf("trigger %q should be dormant, got %q", t2.Name, t2.Status)
		}
		if t2.Suggested == "" {
			t.Fatalf("trigger %q must have a suggestion", t2.Name)
		}
	}
}

func TestBuildRankerLearningAuditClassifiesObservedSources(t *testing.T) {
	root := t.TempDir()
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Source: "eval", Action: "boost", Files: []string{"a.go"}, Score: 0.9,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := comprehension.UpdateLearnedWeights(root, comprehension.UpdateLearnedWeightsOptions{Boost: []string{"a.go"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := buildRankerLearningAudit(root)
	if !got.WeightsFileExists {
		t.Fatalf("weights.json should exist after update")
	}
	if !got.LearningLogExists {
		t.Fatalf("learning log should exist after append")
	}
	if got.WeightCount != 1 {
		t.Fatalf("expected one weight entry, got %d", got.WeightCount)
	}
	if got.LearningSummary == nil || got.LearningSummary.BoostCount != 1 {
		t.Fatalf("learning summary missing boost: %#v", got.LearningSummary)
	}
	if !contains(got.WiredSources, "eval") {
		t.Fatalf("eval should be classified wired, got %v", got.WiredSources)
	}
	for _, t2 := range got.TriggerRecommendations {
		if t2.Name == "eval" && t2.Status != "wired" {
			t.Fatalf("eval trigger should be wired, got %q", t2.Status)
		}
		if t2.Name == "verified_edit" && t2.Status != "dormant" {
			t.Fatalf("verified_edit should be dormant, got %q", t2.Status)
		}
	}
}

func TestRankerLearningQuestionsMentionDormantSources(t *testing.T) {
	root := t.TempDir()
	got := buildRankerLearningAudit(root)
	questions := rankerLearningQuestions(got)
	if len(questions) == 0 {
		t.Fatal("expected questions for dormant sources")
	}
	joined := ""
	for _, q := range questions {
		joined += q.Question + "|"
	}
	for _, want := range []string{"verified_edit", "harness_feedback", "audit_watch"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected question for dormant %q in %s", want, joined)
		}
	}
}

func contains(set []string, want string) bool {
	for _, v := range set {
		if v == want {
			return true
		}
	}
	return false
}
