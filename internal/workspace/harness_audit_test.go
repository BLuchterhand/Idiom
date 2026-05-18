package workspace

import (
	"os"
	"path/filepath"
	"slices"
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
	if !slices.Contains(got.WiredSources, "eval") {
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

func TestBuildRankerLearningAuditClassifiesRecoveryReconciliationSource(t *testing.T) {
	root := t.TempDir()
	if _, err := comprehension.RecordVerifiedRankerEvidence(root, comprehension.VerifiedRankerLearningRequest{
		Source:   comprehension.LearningSourceRecoveryReconciliation,
		Files:    []string{"internal/recovery/packet.go"},
		Score:    1,
		Reason:   comprehension.LearningReasonVerifiedRecovery,
		Verified: true,
		Evidence: comprehension.LearningEvidenceRef{
			Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
			Verified: true,
			Files:    []string{"internal/recovery/packet.go"},
			Metrics: map[string]float64{
				"packet_quality":       1,
				"completed_atom_count": 1,
			},
		},
	}); err != nil {
		t.Fatalf("record recovery learning evidence: %v", err)
	}
	got := buildRankerLearningAudit(root)
	if got.LearningSummary == nil || got.LearningSummary.BySource[comprehension.LearningSourceRecoveryReconciliation] != 1 {
		t.Fatalf("learning summary missing recovery reconciliation source: %#v", got.LearningSummary)
	}
	if !slices.Contains(got.WiredSources, comprehension.LearningSourceRecoveryReconciliation) {
		t.Fatalf("recovery reconciliation should be wired, got %v", got.WiredSources)
	}
	for _, trigger := range got.TriggerRecommendations {
		if trigger.Name == comprehension.LearningSourceRecoveryReconciliation && trigger.Status != "wired" {
			t.Fatalf("recovery reconciliation trigger should be wired, got %#v", trigger)
		}
	}
}

func TestRankerLearningFindingsIgnoreEvidenceOnlyRecoveryNoOps(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		if _, err := comprehension.RecordVerifiedRankerEvidence(root, comprehension.VerifiedRankerLearningRequest{
			Source:   comprehension.LearningSourceRecoveryReconciliation,
			Files:    []string{"internal/recovery/packet.go"},
			Score:    1,
			Reason:   comprehension.LearningReasonVerifiedRecovery,
			Verified: true,
			Evidence: comprehension.LearningEvidenceRef{
				Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
				Verified: true,
				Files:    []string{"internal/recovery/packet.go"},
			},
		}); err != nil {
			t.Fatalf("record recovery learning evidence: %v", err)
		}
	}
	got := buildRankerLearningAudit(root)
	for _, finding := range rankerLearningFindings(got) {
		if finding.ID == "ranker_learning_mostly_noop" {
			t.Fatalf("evidence-only recovery no-op events should not trigger mutable no-op warning: %#v", finding)
		}
	}
}

func TestRankerLearningAuditSurfacesAcceptedRejectedReasons(t *testing.T) {
	root := t.TempDir()
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		EventID: "ranker_accept",
		Source:  "eval",
		Action:  "boost",
		State:   "accepted",
		Reason:  "swe_groundedness_oracle",
	}); err != nil {
		t.Fatalf("append accepted: %v", err)
	}
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		EventID: "ranker_reject",
		Source:  "eval",
		Action:  "no_op",
		State:   "rejected",
		Reason:  "unverified_evidence",
	}); err != nil {
		t.Fatalf("append rejected: %v", err)
	}
	got := buildRankerLearningAudit(root)
	if got.LearningSummary == nil {
		t.Fatalf("learning summary missing")
	}
	if got.LearningSummary.TotalEvents != 2 || got.LearningSummary.AcceptedCount != 1 || got.LearningSummary.RejectedCount != 1 {
		t.Fatalf("learning event counts missing: %#v", got.LearningSummary)
	}
	if got.LearningSummary.AcceptedReasonCounts["swe_groundedness_oracle"] != 1 ||
		got.LearningSummary.RejectedReasonCounts["unverified_evidence"] != 1 {
		t.Fatalf("reason counts missing: %#v", got.LearningSummary)
	}
	summary := harnessAuditSummary(harnessAuditReport{
		Purpose:        "behavior",
		RankerLearning: got,
	})
	if !strings.Contains(summary, "ranker_learning_events=2 accepted=1 rejected=1 reasons=2") {
		t.Fatalf("audit summary missing learning metrics: %s", summary)
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

func TestBuildContextCacheAuditReportsNonCurrentRows(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/auditcache\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package auditcache\n\nfunc X() string { return \"x\" }\n"), 0o644); err != nil {
		t.Fatalf("write x.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	current := comprehension.ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	if err := comprehension.SaveView(root, current, comprehension.PackResult{PackID: current.ID(), IndexID: build.IndexID, CandidateFiles: []comprehension.PackFile{{Path: "x.go"}}}); err != nil {
		t.Fatalf("save current view: %v", err)
	}
	old := comprehension.ViewKey{IndexID: "old-index", Task: "old", Mode: "plan"}
	if err := comprehension.SaveView(root, old, comprehension.PackResult{PackID: old.ID(), IndexID: "old-index", CandidateFiles: []comprehension.PackFile{{Path: "x.go"}}}); err != nil {
		t.Fatalf("save old view: %v", err)
	}
	cache := buildContextCacheAudit(root)
	if cache == nil {
		t.Fatal("expected cache audit")
	}
	if cache.Rows != 2 || cache.Fresh != 2 || cache.Stale != 0 || cache.NonCurrent != 1 || cache.CurrentIndexID != build.IndexID {
		t.Fatalf("expected one non-current row in cache audit, got %#v", cache)
	}
}
