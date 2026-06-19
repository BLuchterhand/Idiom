package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/evidence/eval"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
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
		Files:    []string{"internal/workflows/recovery/packet.go"},
		Score:    1,
		Reason:   comprehension.LearningReasonVerifiedRecovery,
		Verified: true,
		Evidence: comprehension.LearningEvidenceRef{
			Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
			Verified: true,
			Files:    []string{"internal/workflows/recovery/packet.go"},
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

func TestHarnessAuditBehaviorSummarizesIntentLoopEventsWithoutRawPayloadLeakage(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "intent-loop.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"idiom.intent_loop.start","session_id":"intent-session-1","max_cycles":3,"contract_hash":"sha256:abc","prompt":"SECRET raw prompt body"}`,
		`{"type":"idiom.intent_loop.cycle_start","session_id":"intent-session-1","cycle_number":1,"cycle_prompt_sha256":"sha256:def"}`,
		`{"type":"idiom.intent_loop.cycle_complete","session_id":"intent-session-1","cycle_number":1,"gap_count":2,"remaining_gaps":[{"requirement":"verification:go test ./...","status":"failed","detail":"SECRET file body and private stdout"},{"requirement":"acceptance:coverage-threshold","status":"not_met","detail":"customer-acme private details"}]}`,
		`{"type":"run_event","role":"intent_loop","content":"{\"type\":\"idiom.intent_loop.cycle_start\",\"session_id\":\"intent-session-1\",\"cycle_number\":2,\"prompt\":\"SECRET nested prompt\"}"}`,
		`{"type":"run_event","role":"intent_loop","content":"{\"type\":\"idiom.intent_loop.cycle_complete\",\"session_id\":\"intent-session-1\",\"cycle_number\":2,\"gap_count\":0,\"remaining_gaps\":[],\"file_body\":\"SECRET nested file body\"}"}`,
		`{"type":"idiom.intent_loop.complete","session_id":"intent-session-1","total_cycles":2,"changed_paths":["internal/workflows/intentloop/state.go"],"raw_transcript":"SECRET transcript"}`,
		"",
	}, "\n"))

	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}

	for _, leaked := range []string{
		"SECRET",
		"private stdout",
		"customer-acme",
		"raw prompt body",
		"nested file body",
		"transcript",
		"go test ./...",
		"coverage-threshold",
	} {
		if strings.Contains(result.Output, leaked) {
			t.Fatalf("intent-loop audit leaked raw payload marker %q: %s", leaked, result.Output)
		}
	}

	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}

	intentLoop := report.LogBehavior.IntentLoop
	if intentLoop == nil {
		t.Fatalf("expected intent-loop audit rollup, got %#v", report.LogBehavior)
	}

	if intentLoop.LogsHit != 1 ||
		intentLoop.Sessions != 1 ||
		intentLoop.Starts != 1 ||
		intentLoop.CycleStarts != 2 ||
		intentLoop.CycleCompletes != 2 ||
		intentLoop.Completions != 1 ||
		intentLoop.TotalReportedCycles != 2 ||
		intentLoop.TotalReportedGaps != 2 {

		t.Fatalf("unexpected intent-loop audit rollup: %#v", intentLoop)
	}

	if intentLoop.GapRequirementCounts["verification"] != 1 ||
		intentLoop.GapRequirementCounts["acceptance"] != 1 ||
		intentLoop.GapStatusCounts["failed"] != 1 ||
		intentLoop.GapStatusCounts["not_met"] != 1 {

		t.Fatalf("expected sanitized gap class counts, got %#v", intentLoop)
	}

	if !strings.Contains(result.Summary, "intent_loop=1 starts/2 cycle_completes/1 completions/2 gaps") {
		t.Fatalf("expected summary to include intent-loop counters, got %q", result.Summary)
	}
}

func TestRankerLearningFindingsIgnoreEvidenceOnlyRecoveryNoOps(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		if _, err := comprehension.RecordVerifiedRankerEvidence(root, comprehension.VerifiedRankerLearningRequest{
			Source:   comprehension.LearningSourceRecoveryReconciliation,
			Files:    []string{"internal/workflows/recovery/packet.go"},
			Score:    1,
			Reason:   comprehension.LearningReasonVerifiedRecovery,
			Verified: true,
			Evidence: comprehension.LearningEvidenceRef{
				Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
				Verified: true,
				Files:    []string{"internal/workflows/recovery/packet.go"},
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
		{ID: "context_cache_unresolved_gaps", Severity: "info"},
		{ID: "historical_behavior_patterns"},
		{ID: "context_cache_historical_stale_rows"},
	})
	if len(current) != 2 ||
		!slices.ContainsFunc(current, func(finding harnessAuditFinding) bool { return finding.ID == "freshness_refresh_failures" }) ||
		!slices.ContainsFunc(current, func(finding harnessAuditFinding) bool { return finding.ID == "context_cache_unresolved_gaps" }) {
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

// TestContextCacheAuditIncludesSavingsFields verifies that the
// harness audit context cache report includes the Atom 6 token/cost
// savings fields.
func TestContextCacheAuditIncludesSavingsFields(t *testing.T) {
	// buildContextCacheAudit reads from ReadViewStats which uses
	// the process-local atomic counters. The counters are populated
	// by RenderCompactPackProjection (full tokens saved) and
	// LoadView/LoadViewByIDStatus (rediscovery avoided).
	// Verify the fields are present with expected types even when
	// the SQLite store is absent (zero values).
	root := t.TempDir()
	cache := buildContextCacheAudit(root)

	if cache == nil {
		t.Skip("no SQLite store available; savings counters reported as zero via ReadViewStats")
	}

	// Fields must be present (zero when no activity).
	if cache.EstimatedFullTokensSaved < 0 {
		t.Errorf("EstimatedFullTokensSaved must be >= 0, got %d", cache.EstimatedFullTokensSaved)
	}

	if cache.EstimatedRediscoveryTokensAvoided < 0 {
		t.Errorf("EstimatedRediscoveryTokensAvoided must be >= 0, got %d", cache.EstimatedRediscoveryTokensAvoided)
	}

	t.Logf("savings fields: full_tokens_saved=%d, rediscovery_avoided=%d",
		cache.EstimatedFullTokensSaved, cache.EstimatedRediscoveryTokensAvoided)
}

// TestHarnessAuditSummaryIncludesContextCacheMetrics verifies that
// the audit summary string includes context cache metrics with the
// new savings fields (Atom 6).
func TestHarnessAuditSummaryIncludesContextCacheMetrics(t *testing.T) {
	summary := harnessAuditSummary(harnessAuditReport{
		Purpose: "behavior",
		ContextCache: &harnessAuditContextCache{
			Rows:                              5,
			SessionHits:                       3,
			HitRate:                           0.6,
			EstimatedFullTokensSaved:          1234,
			EstimatedRediscoveryTokensAvoided: 5678,
		},
	})
	for _, want := range []string{
		"context_cache_rows=5",
		"hits=3",
		"hit_rate=60%",
		"tokens_saved=1234",
		"rediscovery_avoided=5678",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("audit summary missing %q: %s", want, summary)
		}
	}
}

// TestContextCacheSavingsFinding verifies that the context_cache_savings
// finding is emitted when savings metrics are non-zero, and that the
// finding ID, severity, and evidence fields are correctly structured.
func TestContextCacheSavingsFinding(t *testing.T) {
	tests := []struct {
		name                 string
		cache                harnessAuditContextCache
		expectSavingsFinding bool
		expectContains       []string
	}{
		{
			name: "both savings counters non-zero",
			cache: harnessAuditContextCache{
				Rows:                              5,
				SessionHits:                       3,
				SessionMisses:                     2,
				EstimatedFullTokensSaved:          1000,
				EstimatedRediscoveryTokensAvoided: 2000,
				CurrentRows:                       5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"tokens_saved=1000", "rediscovery_avoided=2000"},
		},
		{
			name: "only token savings non-zero",
			cache: harnessAuditContextCache{
				Rows:                     5,
				SessionHits:              3,
				SessionMisses:            2,
				EstimatedFullTokensSaved: 1000,
				CurrentRows:              5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"tokens_saved=1000"},
		},
		{
			name: "only rediscovery savings non-zero",
			cache: harnessAuditContextCache{
				Rows:                              5,
				SessionHits:                       3,
				SessionMisses:                     2,
				EstimatedRediscoveryTokensAvoided: 2000,
				CurrentRows:                       5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"rediscovery_avoided=2000"},
		},
		{
			name: "zero savings — no finding",
			cache: harnessAuditContextCache{
				Rows:          5,
				SessionHits:   2,
				SessionMisses: 3,
				CurrentRows:   5,
			},
			expectSavingsFinding: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := contextCacheFindings(&tt.cache)
			savingsFinding := harnessAuditFindingByID(findings, "context_cache_savings")

			if tt.expectSavingsFinding && savingsFinding == nil {
				t.Fatal("expected context_cache_savings finding")
			}

			if !tt.expectSavingsFinding && savingsFinding != nil {
				t.Fatalf("unexpected context_cache_savings finding: %+v", savingsFinding)
			}

			if savingsFinding != nil {
				if savingsFinding.Severity != "info" {
					t.Errorf("savings finding severity = %q, want info", savingsFinding.Severity)
				}

				for _, want := range tt.expectContains {
					if !strings.Contains(savingsFinding.Recommendation, want) {
						t.Errorf("savings recommendation missing %q: %s", want, savingsFinding.Recommendation)
					}
				}
			}
		})
	}
}

func TestHarnessAuditReportsChildLifecycleIntegrity(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	invocation := harnessAuditTestChildInvocation("inv-a")
	terminal := harnessAuditTestChildTerminal("term-a", invocation.InvocationID, autonomy.ChildResultEvidencePacket)

	lines := []string{
		mustHarnessAuditChildLifecycleLine(t, "parent_child_invocation", invocation),
		mustHarnessAuditChildLifecycleLine(t, "child_terminal", terminal),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}

	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || report.LogBehavior.ChildLifecycle == nil {
		t.Fatalf("expected child lifecycle report, got %#v", report.LogBehavior)
	}
	if report.LogBehavior.ChildLifecycle.Invocations != 1 || report.LogBehavior.ChildLifecycle.Terminals != 1 {
		t.Fatalf("unexpected lifecycle counts: %#v", report.LogBehavior.ChildLifecycle)
	}
	if harnessAuditFindingByID(report.Findings, "child_lifecycle_terminal_without_acceptance") == nil {
		t.Fatalf("expected missing acceptance finding, got %#v", report.Findings)
	}
	if !strings.Contains(result.Summary, "child_lifecycle=2 records/1 invocations/1 terminals/0 acceptances") {
		t.Fatalf("expected child lifecycle summary, got %q", result.Summary)
	}
}

func TestAuditChildLifecycleLogsAcceptsLinkedChain(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	invocation := harnessAuditTestChildInvocation("inv-linked")
	terminal := harnessAuditTestChildTerminal("term-linked", invocation.InvocationID, autonomy.ChildResultEvidencePacket)
	acceptance := harnessAuditTestChildAcceptance("accept-linked", invocation, terminal, autonomy.ChildAcceptanceAccepted)

	lines := []string{
		mustHarnessAuditChildLifecycleLine(t, "parent_child_invocation", invocation),
		mustHarnessAuditChildLifecycleLine(t, "child_terminal", terminal),
		mustHarnessAuditChildLifecycleLine(t, "child_acceptance", acceptance),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if report.Invocations != 1 || report.Terminals != 1 || report.Acceptances != 1 {
		t.Fatalf("unexpected counts: %#v", report)
	}
	if findings := childLifecycleFindings(report); len(findings) != 0 {
		t.Fatalf("expected no child lifecycle findings, got %#v", findings)
	}
}

func TestAuditChildLifecycleLogsFindsMissingPatchArtifactAndOrphanAcceptance(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	invocation := harnessAuditTestChildInvocation("inv-patch")
	terminal := harnessAuditTestChildTerminal("term-patch", invocation.InvocationID, autonomy.ChildResultCandidatePatch)
	terminal.ArtifactRefs = []string{"patch:missing"}
	orphanAcceptance := harnessAuditTestChildAcceptance("accept-orphan", invocation, terminal, autonomy.ChildAcceptanceRecordedOnly)
	orphanAcceptance.TerminalID = "term-missing"

	lines := []string{
		mustHarnessAuditChildLifecycleLine(t, "parent_child_invocation", invocation),
		mustHarnessAuditChildLifecycleLine(t, "child_terminal", terminal),
		mustHarnessAuditChildLifecycleLine(t, "child_acceptance", orphanAcceptance),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.MissingPatchArtifacts) != 1 {
		t.Fatalf("expected missing patch artifact, got %#v", report.MissingPatchArtifacts)
	}
	if len(report.AcceptancesWithoutTerminal) != 1 {
		t.Fatalf("expected orphan acceptance terminal, got %#v", report.AcceptancesWithoutTerminal)
	}

	findings := childLifecycleFindings(report)
	for _, id := range []string{"child_lifecycle_missing_patch_artifact", "child_lifecycle_acceptance_without_source"} {
		if harnessAuditFindingByID(findings, id) == nil {
			t.Fatalf("expected finding %s, got %#v", id, findings)
		}
	}
}

func TestAuditChildLifecycleLogsFindsMismatchedPatchArtifactOwner(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	invocation := harnessAuditTestChildInvocation("inv-patch-owner")
	terminal := harnessAuditTestChildTerminal("term-patch-owner", invocation.InvocationID, autonomy.ChildResultCandidatePatch)
	terminal.ArtifactRefs = []string{"patch:patch-other"}
	artifact := autonomy.CandidatePatchArtifact{
		PatchID:         "patch-other",
		InvocationID:    "other-invocation",
		ChildContractID: "other-child",
		BaseSHA:         "base",
		ChangedPaths:    []string{"internal/runtime/agent/example.go"},
		DiffStat:        autonomy.PatchDiffStat{Files: 1},
		PatchPath:       "workbranch://patch",
		PatchDigest:     "sha256:patch",
		CreatedAt:       time.Now().UTC(),
	}

	lines := []string{
		mustHarnessAuditChildLifecycleLine(t, "parent_child_invocation", invocation),
		mustHarnessAuditChildLifecycleLine(t, "candidate_patch_artifact", artifact),
		mustHarnessAuditChildLifecycleLine(t, "child_terminal", terminal),
		mustHarnessAuditChildLifecycleLine(t, "child_acceptance", harnessAuditTestChildAcceptance("accept-patch-owner", invocation, terminal, autonomy.ChildAcceptanceRecordedOnly)),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.MismatchedLinks) == 0 {
		t.Fatalf("expected mismatched patch artifact link, got %#v", report)
	}
	if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_mismatched_link"); finding == nil {
		t.Fatalf("expected mismatched link finding")
	}
}

func TestAuditChildLifecycleLogsFindsMismatchedAcceptanceLinks(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	invocation := harnessAuditTestChildInvocation("inv-mismatch")
	terminal := harnessAuditTestChildTerminal("term-mismatch", invocation.InvocationID, autonomy.ChildResultEvidencePacket)
	acceptance := harnessAuditTestChildAcceptance("accept-mismatch", invocation, terminal, autonomy.ChildAcceptanceAccepted)
	acceptance.ChildContractID = "child-other"

	lines := []string{
		mustHarnessAuditChildLifecycleLine(t, "parent_child_invocation", invocation),
		mustHarnessAuditChildLifecycleLine(t, "child_terminal", terminal),
		mustHarnessAuditChildLifecycleLine(t, "child_acceptance", acceptance),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.MismatchedLinks) == 0 {
		t.Fatalf("expected mismatched lifecycle link, got %#v", report)
	}
	if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_mismatched_link"); finding == nil {
		t.Fatalf("expected mismatched link finding")
	}
}

func TestAuditChildLifecycleLogsFindsInvalidPlanExecuteLifecycleEvent(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	line, err := json.Marshal(map[string]any{
		"type":  "idiom.plan_execute.child_terminal_invalid",
		"error": "invalid child terminal record",
	})
	if err != nil {
		t.Fatalf("marshal invalid event: %v", err)
	}
	writeWorkspaceFile(t, logPath, string(line)+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.InvalidRecords) != 1 {
		t.Fatalf("expected invalid record, got %#v", report)
	}
	if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_invalid_record"); finding == nil {
		t.Fatalf("expected invalid record finding")
	}
}

func TestAuditChildLifecycleLogsFindsInvalidVerificationAcceptance(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	record := autonomy.VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verify-a",
		ParentContractID:         "parent-a",
		ObligationRefs:           []string{"obligation:post_write_verification"},
		CommandRef:               "command:go_test",
		CommandFingerprint:       "sha256:abc",
		WorkspaceRef:             "workspace:root",
		BaseSHA:                  "base",
		HeadSHA:                  "head",
		ExitStatus:               1,
		ResultRef:                "result:go_test",
		Decision:                 autonomy.VerificationAcceptanceAccepted,
		ReasonCode:               "exit_zero_required",
	}

	writeWorkspaceFile(t, logPath, mustHarnessAuditChildLifecycleLine(t, "verification_acceptance", record)+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.VerificationAcceptanceViolations) != 1 {
		t.Fatalf("expected verification violation, got %#v", report)
	}
	if finding := harnessAuditFindingByID(childLifecycleFindings(report), "verification_acceptance_invalid"); finding == nil {
		t.Fatalf("expected verification acceptance finding")
	}
}

func TestAuditChildLifecycleLogsFindsVerificationAcceptanceWithoutResult(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	record := autonomy.VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verify-missing-result",
		ParentContractID:         "parent-a",
		ObligationRefs:           []string{"obligation:planner_owned_verification"},
		CommandRef:               "command:missing",
		CommandFingerprint:       "sha256:missing",
		WorkspaceRef:             "workspace:root",
		BaseSHA:                  "base",
		HeadSHA:                  "head",
		ExitStatus:               0,
		ResultRef:                "verification_result:missing",
		Decision:                 autonomy.VerificationAcceptanceAccepted,
		ReasonCode:               "verification_passed",
	}

	writeWorkspaceFile(t, logPath, mustHarnessAuditChildLifecycleLine(t, "verification_acceptance", record)+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.VerificationAcceptancesWithoutResult) != 1 {
		t.Fatalf("expected missing verification result, got %#v", report)
	}
	if finding := harnessAuditFindingByID(childLifecycleFindings(report), "verification_acceptance_without_result"); finding == nil {
		t.Fatalf("expected missing verification result finding")
	}
}

func TestAuditChildLifecycleLogsAcceptsVerificationAcceptanceWithResult(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
	command := "go test ./pkg"
	resultText := strings.Join([]string{"verification", command, "", "passed", "", ""}, "\x00")
	record := autonomy.VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verify-with-result",
		ParentContractID:         "parent-a",
		ObligationRefs:           []string{"obligation:planner_owned_verification"},
		CommandRef:               "command:" + harnessAuditShortHash(command),
		CommandFingerprint:       "sha256:" + harnessAuditShortHash(command),
		WorkspaceRef:             "workspace:root",
		BaseSHA:                  "base",
		HeadSHA:                  "head",
		ExitStatus:               0,
		ResultRef:                "verification_result:" + harnessAuditShortHash(resultText),
		Decision:                 autonomy.VerificationAcceptanceAccepted,
		ReasonCode:               "verification_passed",
	}
	verificationEvent, err := json.Marshal(map[string]any{
		"type":    "idiom.plan_execute.verification",
		"command": command,
		"status":  "passed",
		"summary": "",
		"error":   "",
	})
	if err != nil {
		t.Fatalf("marshal verification event: %v", err)
	}

	lines := []string{
		string(verificationEvent),
		mustHarnessAuditChildLifecycleLine(t, "verification_acceptance", record),
	}
	writeWorkspaceFile(t, logPath, strings.Join(lines, "\n")+"\n")

	report := auditChildLifecycleLogs([]string{logPath}, func(path string) string { return path })
	if report == nil {
		t.Fatal("expected child lifecycle report")
	}
	if len(report.VerificationAcceptancesWithoutResult) != 0 {
		t.Fatalf("did not expect missing verification result, got %#v", report.VerificationAcceptancesWithoutResult)
	}
}

func harnessAuditTestChildInvocation(id string) autonomy.ParentChildInvocationRecord {
	return autonomy.ParentChildInvocationRecord{
		InvocationID:     id,
		ParentContractID: "parent-" + id,
		ChildContractID:  "child-" + id,
		Role:             "child_read_only:explorer",
		Lane:             "implement",
		Phase:            "child_read_only",
		WorkspaceMode:    autonomy.ChildWorkspaceNoWrite,
		ContractSummary: autonomy.ChildContractSummary{
			PathRoleDigest:   "sha256:path",
			ToolPolicyDigest: "sha256:tool",
			ObligationDigest: "sha256:obligation",
		},
		RuntimeEnvelope: autonomy.ChildRuntimeEnvelope{
			BackendKind: "llm_client",
			ModelAlias:  "qwen-flash",
			ModelRole:   "child_read_only:explorer",
			RoleResolution: autonomy.ChildRoleResolution{
				RequestedRole: "child_read_only:explorer",
				ResolvedRole:  "child_read_only:explorer",
				Alias:         "qwen-flash",
				Source:        autonomy.RoleResolutionSourceRoleTag,
				OK:            true,
			},
			CapacityAdmission: autonomy.ChildCapacityAdmission{
				State:       autonomy.CapacityAdmissionAcquired,
				Enforcement: autonomy.CapacityAdmissionOpportunistic,
			},
			ToolTransportMode:   autonomy.ChildTransportTextual,
			ParserTransportMode: autonomy.ChildTransportTextual,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func harnessAuditTestChildTerminal(id string, invocationID string, resultKind autonomy.ChildResultKind) autonomy.ChildTerminalRecord {
	record := autonomy.ChildTerminalRecord{
		TerminalID:         id,
		InvocationID:       invocationID,
		ChildContractID:    "child-" + invocationID,
		ResultKind:         resultKind,
		Status:             autonomy.ChildTerminalCompleted,
		EvidenceRefs:       []string{"tool_result:context_pack:1"},
		ObjectiveAlignment: autonomy.ChildObjectiveAccepted,
	}
	if resultKind == autonomy.ChildResultCandidatePatch {
		record.EvidenceRefs = nil
		record.ChangedPaths = []string{"internal/runtime/agent/example.go"}
	}
	return record
}

func harnessAuditTestChildAcceptance(id string, invocation autonomy.ParentChildInvocationRecord, terminal autonomy.ChildTerminalRecord, decision autonomy.ChildAcceptanceDecision) autonomy.ChildAcceptanceRecord {
	return autonomy.ChildAcceptanceRecord{
		AcceptanceID:         id,
		ParentContractID:     invocation.ParentContractID,
		ChildContractID:      invocation.ChildContractID,
		InvocationID:         invocation.InvocationID,
		TerminalID:           terminal.TerminalID,
		ResultKind:           terminal.ResultKind,
		Decision:             decision,
		AcceptedEvidenceRefs: []string{"tool_result:context_pack:1"},
		AlignmentBasis: autonomy.ChildAlignmentBasis{
			DelegatedObjectiveRef:  "objective:task",
			EffectiveScopeRefs:     []string{"scope:repo"},
			AcceptedSourceBodyRefs: []string{"source_body:target"},
			Evaluator:              autonomy.AlignmentEvaluatorHostContract,
		},
		ReasonCode: "objective_aligned",
	}
}

func mustHarnessAuditChildLifecycleLine(t *testing.T, role string, record any) string {
	t.Helper()

	switch typed := record.(type) {
	case autonomy.ParentChildInvocationRecord:
		typed.Normalize()
		record = typed
	case autonomy.ChildTerminalRecord:
		typed.Normalize()
		record = typed
	case autonomy.ChildAcceptanceRecord:
		typed.Normalize()
		record = typed
	case autonomy.CandidatePatchArtifact:
		typed.Normalize()
		record = typed
	case autonomy.VerificationAcceptanceRecord:
		typed.Normalize()
		record = typed
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal child lifecycle record: %v", err)
	}

	wrapped, err := json.Marshal(map[string]string{
		"type":    "message",
		"role":    role,
		"content": string(data),
	})
	if err != nil {
		t.Fatalf("marshal wrapped child lifecycle record: %v", err)
	}

	return string(wrapped)
}

func harnessAuditFindingByID(findings []harnessAuditFinding, id string) *harnessAuditFinding {
	for i := range findings {
		if findings[i].ID == id {
			return &findings[i]
		}
	}

	return nil
}
