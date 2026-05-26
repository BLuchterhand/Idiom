package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/comprehension"
	"idiom/internal/eval"
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

func TestHarnessAuditSummaryIncludesExtendedGateCoverage(t *testing.T) {
	summary := harnessAuditSummary(harnessAuditReport{
		Purpose: "behavior",
		ContractCoverage: &autonomy.EventAuditReport{
			TotalEvents: 3,
			GateCoverage: autonomy.GateEventCoverage{
				DecisionEvents:                  2,
				DecisionCountsByPackID:          map[string]int{"pack-alpha": 2},
				DecisionCountsByEnforcementMode: map[string]int{"enforce": 1, "observe_only": 1},
				EventsWithObservedFacts:         1,
				EventsWithSafeAlternatives:      1,
				MissingPackIDRefs:               []autonomy.EventAuditRef{{Reference: "pack_id"}},
			},
		},
	})

	for _, want := range []string{
		"contract_events=3",
		"gate_decisions=2 packs=1 enforcement_modes=2 observed_facts=1 safe_alternatives=1 missing_gate_fields=1",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("audit summary missing %q: %s", want, summary)
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

func TestRankerLearningIdleFindingExplainsExpectedFreshState(t *testing.T) {
	findings := rankerLearningFindings(&harnessAuditRankerLearning{})
	finding := harnessAuditFindingByID(findings, "ranker_learning_idle")
	if finding == nil {
		t.Fatalf("expected idle ranker finding, got %#v", findings)
	}
	if !strings.Contains(finding.Evidence, "expected before") || !strings.Contains(finding.Recommendation, "no action needed") {
		t.Fatalf("idle ranker finding should not imply a broken system: %#v", finding)
	}
}

func TestRankerLearningDormantSourcesStayInRecommendations(t *testing.T) {
	findings := rankerLearningFindings(&harnessAuditRankerLearning{
		WeightsFileExists: true,
		LearningLogExists: true,
		DormantSources:    []string{"verified_edit"},
	})
	if harnessAuditFindingByID(findings, "ranker_learning_dormant_sources") != nil {
		t.Fatalf("dormant trigger recommendations should not be top-level findings: %#v", findings)
	}
}

func TestSplitHistoricalHarnessAuditFindings(t *testing.T) {
	current, historical := splitHistoricalHarnessAuditFindings([]harnessAuditFinding{
		{ID: "freshness_refresh_failures"},
		{ID: "historical_behavior_patterns"},
		{ID: "context_cache_historical_stale_rows"},
	})
	if len(current) != 1 || current[0].ID != "freshness_refresh_failures" {
		t.Fatalf("unexpected current findings: %#v", current)
	}
	if len(historical) != 2 {
		t.Fatalf("unexpected historical findings: %#v", historical)
	}
}

func TestFreshnessHealthFindingsIgnoreLogDerivedHistory(t *testing.T) {
	historical := buildFreshnessHealthAudit(&eval.ContextStats{
		FreshnessBlockers:        1,
		FreshnessRefreshFailures: 1,
		StaleUseBlocked:          1,
	}, nil, true)
	current := buildFreshnessHealthAudit(nil, nil, false)
	if len(freshnessHealthFindings(&current)) != 0 {
		t.Fatalf("empty current freshness health should not report findings")
	}
	if historical.TelemetryBlockers != 1 || historical.StaleUseBlocked != 1 {
		t.Fatalf("historical telemetry should remain in freshness health: %#v", historical)
	}
	if findings := freshnessHealthFindings(&historical); len(findings) != 0 {
		t.Fatalf("historical-only telemetry should not raise current findings: %#v", findings)
	}
}

func TestFreshnessHealthFindingsReportCurrentLogDerivedBlockers(t *testing.T) {
	findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{
		TelemetryBlockers:        1,
		TelemetryRefreshFailures: 1,
		TelemetryAllowStale:      1,
		StaleUseBlocked:          1,
	})
	for _, id := range []string{
		"freshness_allow_stale_invoked",
		"freshness_refresh_failures",
		"freshness_log_blockers",
		"stale_use_blockers_observed",
	} {
		if harnessAuditFindingByID(findings, id) == nil {
			t.Fatalf("expected current log-derived finding %q, got %#v", id, findings)
		}
	}
}

func TestFreshnessHealthFindingsIgnoreRecoveredCurrentLogBlockers(t *testing.T) {
	health := buildFreshnessHealthAudit(&eval.ContextStats{
		FreshnessBlockers:        1,
		FreshnessRefreshFailures: 1,
		StaleUseBlocked:          1,
	}, &harnessAuditFreshnessWindow{
		LatestState:               "fresh",
		ResolvedBlockers:          1,
		ResolvedRefreshFailures:   1,
		ResolvedStaleUse:          1,
		UnresolvedBlockers:        0,
		UnresolvedRefreshFailures: 0,
		UnresolvedStaleUse:        0,
	}, false)
	if health.TelemetryBlockers != 0 || health.TelemetryRefreshFailures != 0 || health.StaleUseBlocked != 0 {
		t.Fatalf("recovered blockers should not remain unresolved: %#v", health)
	}
	if health.TelemetryResolvedBlockers != 1 || health.TelemetryResolvedRefreshFailures != 1 ||
		health.ResolvedStaleUseBlocked != 1 || health.TelemetryLatestState != "fresh" {
		t.Fatalf("recovered blockers should remain visible in health: %#v", health)
	}
	if findings := freshnessHealthFindings(&health); len(findings) != 0 {
		t.Fatalf("recovered blockers should not raise active findings: %#v", findings)
	}
}

func TestAuditCurrentFreshnessWindowTreatsLaterFreshResultAsRecovery(t *testing.T) {
	window := auditCurrentFreshnessWindow("", []eval.PerfReport{
		{
			LogPath: "a.jsonl",
			Context: &eval.ContextStats{
				FreshnessBlockers:        1,
				FreshnessRefreshFailures: 1,
				StaleUseBlocked:          1,
			},
		},
		{
			LogPath: "b.jsonl",
			Context: &eval.ContextStats{
				FreshnessReadThroughResults: 1,
				FreshResults:                1,
				StaleUseFresh:               1,
			},
		},
	})
	if window == nil {
		t.Fatal("expected freshness window")
	}
	if window.LatestState != "fresh" || window.UnresolvedBlockers != 0 ||
		window.UnresolvedRefreshFailures != 0 || window.UnresolvedStaleUse != 0 {
		t.Fatalf("later fresh result should clear unresolved blockers: %#v", window)
	}
	if window.ResolvedBlockers != 1 || window.ResolvedRefreshFailures != 1 ||
		window.ResolvedStaleUse != 1 || window.FreshResultsAfterBlocker != 1 {
		t.Fatalf("recovered blockers should be retained as resolved evidence: %#v", window)
	}
}

func TestFreshnessHealthFindingsReportInProcessRefreshFailures(t *testing.T) {
	findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{RefreshFailures: 1})
	if harnessAuditFindingByID(findings, "freshness_refresh_failures") == nil {
		t.Fatalf("in-process refresh failure should remain actionable, got %#v", findings)
	}
}

func TestFreshnessHealthFindingsEvidenceFiltersHistoricalOnlyTelemetry(t *testing.T) {
	findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{
		RefreshFailures:          1,
		TelemetryRefreshFailures: 3,
		AllowStaleInvocations:    1,
		TelemetryAllowStale:      2,
		TelemetryHistoricalOnly:  true,
	})
	refresh := harnessAuditFindingByID(findings, "freshness_refresh_failures")
	if refresh == nil || !strings.Contains(refresh.Evidence, "1 in-process and 0 current log-derived") {
		t.Fatalf("refresh evidence should not label historical telemetry as current: %#v", findings)
	}
	allowStale := harnessAuditFindingByID(findings, "freshness_allow_stale_invoked")
	if allowStale == nil || !strings.Contains(allowStale.Evidence, "1 in-process and 0 current log-derived") {
		t.Fatalf("allow-stale evidence should not label historical telemetry as current: %#v", findings)
	}
}

func TestHarnessAuditPerfReportTimeFallsBackToLogTimestamp(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "tool-context_pack-latest.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte(`{"ts":"2026-05-24T11:35:23.495627-04:00","type":"tool_result"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	got, ok := harnessAuditPerfReportTime(root, eval.PerfReport{
		SessionID: "tool-context_pack-latest",
		LogPath:   "logs/telemetry/tool-context_pack-latest.jsonl",
	})
	if !ok {
		t.Fatal("expected timestamp from log body")
	}
	want := time.Date(2026, 5, 24, 15, 35, 23, 495627000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
}

func TestHarnessAuditPerfReportTimePrefersLogTimestampOverLocalFilename(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "conversations", "20260526-040000.000.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte(`{"ts":"2026-05-26T04:00:00-04:00","type":"session_start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	got, ok := harnessAuditPerfReportTime(root, eval.PerfReport{
		SessionID: "20260526-040000",
		LogPath:   "logs/conversations/20260526-040000.000.jsonl",
	})
	if !ok {
		t.Fatal("expected timestamp from log body")
	}
	want := time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
}

func TestContextCacheFindingsTreatHistoricalStaleRowsAsHistory(t *testing.T) {
	findings := contextCacheFindings(&harnessAuditContextCache{
		Rows:              10,
		Fresh:             1,
		Stale:             9,
		CurrentRows:       1,
		CurrentFresh:      1,
		CurrentStale:      0,
		HistoricalRows:    9,
		StaleRatio:        0.9,
		CurrentStaleRatio: 0,
	})
	if harnessAuditFindingByID(findings, "context_cache_mostly_stale") != nil {
		t.Fatalf("historical stale rows should not report active cache as mostly stale: %#v", findings)
	}
	finding := harnessAuditFindingByID(findings, "context_cache_historical_stale_rows")
	if finding == nil {
		t.Fatalf("expected historical stale row finding, got %#v", findings)
	}
	if !strings.Contains(finding.Evidence, "historical") || !strings.Contains(finding.Evidence, "current-index rows: fresh=1 stale=0") {
		t.Fatalf("historical stale finding should explain active cache state: %#v", finding)
	}
}

func TestContextCacheFindingsReportCurrentStaleRowsAsActionable(t *testing.T) {
	findings := contextCacheFindings(&harnessAuditContextCache{
		Rows:              4,
		Fresh:             1,
		Stale:             3,
		CurrentRows:       4,
		CurrentFresh:      1,
		CurrentStale:      3,
		StaleRatio:        0.75,
		CurrentStaleRatio: 0.75,
	})
	finding := harnessAuditFindingByID(findings, "context_cache_mostly_stale")
	if finding == nil {
		t.Fatalf("expected active stale cache finding, got %#v", findings)
	}
	if finding.Severity != "low" || !strings.Contains(finding.Evidence, "current-index") {
		t.Fatalf("current stale finding should be scoped to current rows: %#v", finding)
	}
}

func TestContextCacheFindingsIgnoreHistoricalContractGap(t *testing.T) {
	findings := contextCacheFindings(&harnessAuditContextCache{
		Rows:                2,
		ContractRows:        1,
		CurrentRows:         1,
		CurrentContractRows: 1,
		HistoricalRows:      1,
	})
	if harnessAuditFindingByID(findings, "context_cache_contract_gap") != nil {
		t.Fatalf("historical no-contract rows should not raise active contract gap: %#v", findings)
	}
}

func TestContextCacheFindingsReportCurrentContractGap(t *testing.T) {
	findings := contextCacheFindings(&harnessAuditContextCache{
		Rows:                2,
		ContractRows:        1,
		CurrentRows:         1,
		CurrentContractRows: 0,
		HistoricalRows:      1,
	})
	finding := harnessAuditFindingByID(findings, "context_cache_contract_gap")
	if finding == nil {
		t.Fatalf("current no-contract row should raise active contract gap, got %#v", findings)
	}
	if !strings.Contains(finding.Evidence, "current graph_views") {
		t.Fatalf("contract gap evidence should be scoped to current rows: %#v", finding)
	}
}

func TestContextCacheFindingsExplainCurrentUnresolvedGapReasons(t *testing.T) {
	findings := contextCacheFindings(&harnessAuditContextCache{
		Rows:                                  8,
		Fresh:                                 2,
		Stale:                                 6,
		CurrentRows:                           2,
		CurrentFresh:                          2,
		UnresolvedGapRows:                     8,
		CurrentUnresolvedGapRows:              2,
		CurrentFreshUnresolvedGapRows:         2,
		UnresolvedGapReasonCounts:             map[string]int{"stale_runtime_trace": 8},
		CurrentUnresolvedGapReasonCounts:      map[string]int{"stale_runtime_trace": 2},
		CurrentFreshUnresolvedGapReasonCounts: map[string]int{"stale_runtime_trace": 2},
	})
	finding := harnessAuditFindingByID(findings, "context_cache_unresolved_gaps")
	if finding == nil {
		t.Fatalf("expected current unresolved gap finding, got %#v", findings)
	}
	if !strings.Contains(finding.Evidence, "current fresh") || !strings.Contains(finding.Evidence, "stale_runtime_trace=2") {
		t.Fatalf("unresolved gap finding should explain current reason counts: %#v", finding)
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
	if cache.CurrentRows != 1 || cache.CurrentFresh != 1 || cache.CurrentStale != 0 || cache.HistoricalRows != 1 {
		t.Fatalf("expected current/historical rows in cache audit, got %#v", cache)
	}
}

func harnessAuditFindingByID(findings []harnessAuditFinding, id string) *harnessAuditFinding {
	for i := range findings {
		if findings[i].ID == id {
			return &findings[i]
		}
	}
	return nil
}
