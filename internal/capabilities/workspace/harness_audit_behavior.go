package workspace

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/evidence/eval"
	"idiom/internal/platform/safepath"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

// Older local conversation logs remain available as historical trend
// evidence, but they should not fail current behavior audits.
var harnessAuditBehaviorFindingsIntroducedAt = time.Date(2026, 6, 18, 13, 55, 0, 0, time.UTC)

type harnessAuditLogBehavior struct {
	LogLimit            int                             `json:"log_limit"`
	LogsConsidered      int                             `json:"logs_considered"`
	LogsScanned         int                             `json:"logs_scanned"`
	CurrentLogsScanned  int                             `json:"current_logs_scanned"`
	Sources             []harnessAuditLogSource         `json:"sources,omitempty"`
	Warnings            []string                        `json:"warnings,omitempty"`
	Aggregate           eval.PerfAggregate              `json:"aggregate"`
	CurrentAggregate    eval.PerfAggregate              `json:"-"`
	FreshnessResolution *harnessAuditFreshnessWindow    `json:"freshness_resolution,omitempty"`
	TopPatterns         []eval.PerfPatternHit           `json:"top_patterns,omitempty"`
	TopTools            []eval.PerfToolMix              `json:"top_tools,omitempty"`
	CapabilityMix       []harnessAuditCapabilityMix     `json:"capability_mix,omitempty"`
	TopCapabilities     []harnessAuditCapabilityMix     `json:"top_capabilities,omitempty"`
	WorstOffenders      []eval.PerfWorstOffender        `json:"worst_offenders,omitempty"`
	MalformedOutput     *harnessAuditMalformedOutput    `json:"malformed_output,omitempty"`
	IntentLoop          *harnessAuditIntentLoop         `json:"intent_loop,omitempty"`
	FeaturePlanner      *harnessAuditFeaturePlanner     `json:"feature_planner,omitempty"`
	GuardFriction       *autonomy.GuardFrictionCoverage `json:"guard_friction,omitempty"`
	ChildLifecycle      *harnessAuditChildLifecycle     `json:"child_lifecycle,omitempty"`
	LaneHistory         *harnessAuditLaneHistory        `json:"lane_history,omitempty"`
	InProgressSessions  []harnessAuditInProgressSession `json:"in_progress_sessions,omitempty"`
	IncompleteSessions  []harnessAuditIncompleteSession `json:"incomplete_sessions,omitempty"`
}

type harnessAuditCapabilityMix struct {
	Capability string  `json:"capability"`
	Calls      int     `json:"calls"`
	Share      float64 `json:"share"`
	AvgPerLog  float64 `json:"avg_per_log"`
}

type harnessAuditMalformedOutput struct {
	LogsHit                int            `json:"logs_hit,omitempty"`
	EventCounts            map[string]int `json:"event_counts,omitempty"`
	ValidationResultCounts map[string]int `json:"validation_result_counts,omitempty"`
	FailureClassCounts     map[string]int `json:"failure_class_counts,omitempty"`
	CompileOutcomeCounts   map[string]int `json:"compile_outcome_counts,omitempty"`
	RecoveryOutcomeCounts  map[string]int `json:"recovery_outcome_counts,omitempty"`
	SchemaVersionCounts    map[string]int `json:"schema_version_counts,omitempty"`
}

func (m *Manager) auditLogBehavior(req ToolRequest) (*harnessAuditLogBehavior, *autonomy.EventAuditReport, []harnessAuditFinding, []harnessAuditQuestion, error) {
	limit := harnessAuditLogLimit(req.Limit)

	paths, sources, warnings, err := m.auditLogPaths(req, limit)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	reports := make([]eval.PerfReport, 0, len(paths))
	for _, path := range paths {
		file, err := safepath.OpenFileUnderRoot(m.cfg.Root, path, "audit log", safepath.Options{})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.pathAccess.DisplayPath(path), err))
			continue
		}
		report, err := eval.AnalyzeLogReader(path, file)
		closeErr := file.Close()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.pathAccess.DisplayPath(path), err))
			continue
		}
		if closeErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.pathAccess.DisplayPath(path), closeErr))
			continue
		}

		report.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		report.LogPath = m.pathAccess.DisplayPath(path)
		reports = append(reports, report)
	}

	incompleteSessions, inProgressSessions, lifecycleWarnings := m.auditIncompleteImplementSessions(paths)

	warnings = append(warnings, lifecycleWarnings...)

	contractCoverage, contractWarnings := m.auditContractEventCoverageWithFilter(paths, nil)
	currentContractCoverage, currentContractWarnings := m.auditCurrentContractEventCoverage(paths)

	warnings = append(warnings, contractWarnings...)

	childLifecycle := auditChildLifecycleLogs(m.cfg.Root, paths, m.pathAccess.DisplayPath)

	malformedOutput, malformedWarnings := auditMalformedOutputStages(m.cfg.Root, paths)

	for _, warning := range malformedWarnings {
		warnings = append(warnings, fmt.Sprintf("malformed output stages: %s", warning))
	}

	intentLoop := auditIntentLoopEvents(m.cfg.Root, paths)
	featurePlanner := auditFeaturePlannerEvents(m.cfg.Root, paths, m.pathAccess.DisplayPath)
	aggregate := eval.Aggregate(reports)
	currentReports := currentHarnessAuditPerfReports(m.cfg.Root, reports)
	currentAggregate := eval.Aggregate(currentReports)
	freshnessResolution := auditCurrentFreshnessWindow(m.cfg.Root, currentReports)
	capabilityMix := auditObservedCapabilityMix(m.cfg.Root, paths, aggregate.LogCount, &warnings, m.pathAccess.DisplayPath)
	behavior := &harnessAuditLogBehavior{
		LogLimit:            limit,
		LogsConsidered:      len(paths),
		LogsScanned:         len(reports),
		CurrentLogsScanned:  len(currentReports),
		Sources:             sources,
		Warnings:            warnings,
		Aggregate:           aggregate,
		CurrentAggregate:    currentAggregate,
		FreshnessResolution: freshnessResolution,
		TopPatterns:         firstAuditPatterns(aggregate.PatternHitRate, 8),
		TopTools:            firstAuditTools(aggregate.ToolMix, 10),
		CapabilityMix:       capabilityMix,
		TopCapabilities:     firstAuditCapabilities(capabilityMix, 10),
		WorstOffenders:      aggregate.WorstOffenders,
		MalformedOutput:     malformedOutput,
		IntentLoop:          intentLoop,
		FeaturePlanner:      featurePlanner,
		GuardFriction:       harnessAuditGuardFrictionCoverage(contractCoverage),
		ChildLifecycle:      childLifecycle,
		LaneHistory:         buildHarnessAuditLaneHistory(m.cfg.Root, 8),
		InProgressSessions:  inProgressSessions,
		IncompleteSessions:  incompleteSessions,
	}
	findings := logBehaviorFindings(currentAggregate, incompleteSessions)

	findings = append(findings, historicalLogBehaviorFindings(aggregate, currentAggregate)...)
	findings = append(findings, childLifecycleFindings(childLifecycle)...)
	findings = append(findings, historicalTurnPhaseProjectionFindings(m.cfg.Root, paths)...)
	findings = append(findings, historicalOpenEvidenceFinalAnswerProjectionFindings(m.cfg.Root, paths)...)
	findings = append(findings, contractCoverageWarningFindingsWithID("contract_event_invalid", currentContractWarnings)...)
	findings = append(findings, historicalContractCoverageWarningFindings(contractWarnings, currentContractWarnings)...)
	findings = append(findings, contractCoverageFindings(currentContractCoverage)...)
	findings = append(findings, featurePlannerLifecycleFindings(featurePlanner)...)

	return behavior, contractCoverage, findings, logBehaviorQuestions(aggregate), nil
}

var malformedOutputAuditRoles = map[string]struct{}{
	"tool_request_normalizer":             {},
	"malformed_output_detected":           {},
	"artifact_distillation_started":       {},
	"artifact_distillation_result":        {},
	"edit_intent_validated":               {},
	"patch_compile_started":               {},
	"patch_compile_result":                {},
	"executable_artifact_validated":       {},
	"malformed_output_recovery_exhausted": {},
}

func auditMalformedOutputStages(root string, paths []string) (*harnessAuditMalformedOutput, []string) {
	out := &harnessAuditMalformedOutput{
		EventCounts:            map[string]int{},
		ValidationResultCounts: map[string]int{},
		FailureClassCounts:     map[string]int{},
		CompileOutcomeCounts:   map[string]int{},
		RecoveryOutcomeCounts:  map[string]int{},
		SchemaVersionCounts:    map[string]int{},
	}

	var warnings []string

	logsHit := map[string]struct{}{}

	for _, path := range paths {
		body, err := safepath.ReadFileUnderRoot(root, path, "audit log", safepath.Options{})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}

		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			role, content, ok := auditRunEventRoleAndContent([]byte(line))
			if !ok {
				continue
			}

			if _, tracked := malformedOutputAuditRoles[role]; !tracked {
				continue
			}

			out.EventCounts[role]++

			logsHit[path] = struct{}{}

			auditMalformedOutputContent(out, role, content)
		}
	}

	out.LogsHit = len(logsHit)
	if out.LogsHit == 0 {
		return nil, warnings
	}

	pruneEmptyAuditCounts(out)

	return out, warnings
}

func auditRunEventRoleAndContent(line []byte) (string, string, bool) {
	var row map[string]any

	if err := json.Unmarshal(line, &row); err != nil {
		return "", "", false
	}

	role := auditString(row["role"])
	content := auditString(row["content"])

	if role == "" {
		if data, ok := row["data"].(map[string]any); ok {
			role = auditString(data["role"])
			content = auditString(data["content"])
		}
	}

	if role == "" {
		return "", "", false
	}

	return role, content, true
}

func auditMalformedOutputContent(out *harnessAuditMalformedOutput, role string, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return
	}

	validationKeys := []string{"validation_result"}
	if role == "edit_intent_validated" {
		validationKeys = append(validationKeys, "result")
	}

	for _, key := range validationKeys {
		if value := auditString(payload[key]); value != "" {
			out.ValidationResultCounts[value]++
			break
		}
	}

	for _, key := range []string{"failure_class", "parser_failure_class", "error_code"} {
		if value := auditString(payload[key]); value != "" {
			out.FailureClassCounts[value]++
			break
		}
	}

	if role == "patch_compile_started" || role == "patch_compile_result" {
		if value := auditString(payload["compile_outcome"]); value != "" {
			out.CompileOutcomeCounts[value]++
		}
	}

	if role == "malformed_output_recovery_exhausted" {
		if value := auditString(payload["recovery_outcome"]); value != "" {
			out.RecoveryOutcomeCounts[value]++
		}
	}

	if value := auditString(payload["schema_version"]); value != "" {
		out.SchemaVersionCounts[value]++
	}
}

func pruneEmptyAuditCounts(out *harnessAuditMalformedOutput) {
	if len(out.EventCounts) == 0 {
		out.EventCounts = nil
	}

	if len(out.ValidationResultCounts) == 0 {
		out.ValidationResultCounts = nil
	}

	if len(out.FailureClassCounts) == 0 {
		out.FailureClassCounts = nil
	}

	if len(out.CompileOutcomeCounts) == 0 {
		out.CompileOutcomeCounts = nil
	}

	if len(out.RecoveryOutcomeCounts) == 0 {
		out.RecoveryOutcomeCounts = nil
	}

	if len(out.SchemaVersionCounts) == 0 {
		out.SchemaVersionCounts = nil
	}
}

func logBehaviorFindings(aggregate eval.PerfAggregate, incompleteSessions []harnessAuditIncompleteSession) []harnessAuditFinding {
	var findings []harnessAuditFinding

	for _, pattern := range aggregate.PatternHitRate {
		if pattern.LogsHit == 0 {
			continue
		}

		severity := "low"
		if pattern.HitRate >= 0.5 || pattern.TotalCount >= 5 {
			severity = "medium"
		}

		findings = append(findings, harnessAuditFinding{
			ID:       "behavior_" + pattern.Name,
			Severity: severity,
			Evidence: fmt.Sprintf("%s appeared in %d log(s), %d total hit(s)", pattern.Name, pattern.LogsHit, pattern.TotalCount),
		})
	}

	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		severity := "low"
		if aggregate.Feedback.HighSeverity > 0 {
			severity = "high"
		} else if aggregate.Feedback.MediumSeverity > 0 || aggregate.Feedback.Total >= 3 {
			severity = "medium"
		}

		findings = append(findings, harnessAuditFinding{
			ID:             "agent_harness_feedback_reported",
			Severity:       severity,
			Evidence:       fmt.Sprintf("%d feedback event(s), %d verified, top category %s, top component %s", aggregate.Feedback.Total, aggregate.Feedback.Verified, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Recommendation: "Triage structured feedback before adding scenario-specific prompt text; prefer generic tool, telemetry, or contract fixes.",
			Tools:          []string{"harness_feedback", "harness_audit"},
		})
	}

	if aggregate.LocalOps != nil {
		if aggregate.LocalOps.QueueDegraded > 0 || aggregate.LocalOps.ContextBudgetDegraded > 0 || aggregate.LocalOps.ClassifierDegraded > 0 || aggregate.LocalOps.DoctorDegradedProfiles > 0 || aggregate.LocalOps.DoctorBlockedProfiles > 0 {
			severity := "low"
			if aggregate.LocalOps.DoctorBlockedProfiles > 0 {
				severity = "high"
			} else if aggregate.LocalOps.QueueDegraded > 0 || aggregate.LocalOps.ContextBudgetDegraded > 0 {
				severity = "medium"
			}

			findings = append(findings, harnessAuditFinding{
				ID:             "local_ops_degraded_mode_reported",
				Severity:       severity,
				Evidence:       fmt.Sprintf("queue_degraded=%d context_budget_degraded=%d classifier_degraded=%d doctor_degraded=%d doctor_blocked=%d", aggregate.LocalOps.QueueDegraded, aggregate.LocalOps.ContextBudgetDegraded, aggregate.LocalOps.ClassifierDegraded, aggregate.LocalOps.DoctorDegradedProfiles, aggregate.LocalOps.DoctorBlockedProfiles),
				Recommendation: "Use doctor readiness and queue/status telemetry to decide whether to keep degraded local operation report-only or add profile gates.",
				Tools:          []string{"harness_audit", "idiom doctor", "idiom queue"},
			})
		}
	}

	if len(incompleteSessions) > 0 {
		currentIncompleteSessions := currentHarnessAuditIncompleteSessions(incompleteSessions)
		if len(currentIncompleteSessions) == 0 {
			findings = append(findings, harnessAuditFinding{
				ID:             "historical_incomplete_implement_session_lifecycle",
				Severity:       "info",
				Evidence:       fmt.Sprintf("%d pre-closeout implementation-backed session(s) missing process/session lifecycle event(s)", len(incompleteSessions)),
				Recommendation: "Historical lifecycle gaps are retained in log_behavior.incomplete_sessions; current sessions still raise incomplete_implement_session_lifecycle.",
				Tools:          []string{"harness_audit"},
			})

			return findings
		}

		findings = append(findings, harnessAuditFinding{
			ID:             "incomplete_implement_session_lifecycle",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d implementation-backed session(s) missing process/session lifecycle event(s)", len(currentIncompleteSessions)),
			Recommendation: "Inspect termination reasons and keep lifecycle closeout generic: every implementation-backed run should end with process_exit telemetry and a session_summary conversation event.",
			Tools:          []string{"harness_audit"},
		})
	}

	return findings
}

func currentHarnessAuditPerfReports(root string, reports []eval.PerfReport) []eval.PerfReport {
	var current []eval.PerfReport

	for _, report := range reports {
		if harnessAuditPerfReportIsSelfAudit(report) {
			continue
		}

		occurredAt, ok := harnessAuditPerfReportTime(root, report)
		if !ok || !occurredAt.Before(harnessAuditBehaviorFindingsIntroducedAt) {
			current = append(current, report)
		}
	}

	return current
}

func harnessAuditPerfReportIsSelfAudit(report eval.PerfReport) bool {
	if report.Tools.ByName["harness_audit"] > 0 {
		return true
	}

	return strings.Contains(filepath.Base(report.LogPath), "tool:harness_audit")
}

func auditCurrentFreshnessWindow(root string, reports []eval.PerfReport) *harnessAuditFreshnessWindow {
	type timedReport struct {
		report     eval.PerfReport
		occurredAt time.Time
		knownTime  bool
	}

	timed := make([]timedReport, 0, len(reports))

	for _, report := range reports {
		if report.Context == nil {
			continue
		}

		occurredAt, ok := harnessAuditPerfReportTime(root, report)

		timed = append(timed, timedReport{report: report, occurredAt: occurredAt, knownTime: ok})
	}

	if len(timed) == 0 {
		return nil
	}

	sort.SliceStable(timed, func(i, j int) bool {
		left, right := timed[i], timed[j]
		if left.knownTime && right.knownTime {
			return left.occurredAt.Before(right.occurredAt)
		}

		if left.knownTime != right.knownTime {
			return !left.knownTime
		}

		return left.report.LogPath < right.report.LogPath
	})

	var window harnessAuditFreshnessWindow

	for _, item := range timed {
		ctx := item.report.Context
		blockers := ctx.FreshnessBlockers
		staleUse := ctx.StaleUseBlocked
		refreshFailures := ctx.FreshnessRefreshFailures

		if blockers > 0 || staleUse > 0 || refreshFailures > 0 {
			window.UnresolvedBlockers += blockers
			window.UnresolvedStaleUse += staleUse
			window.UnresolvedRefreshFailures += refreshFailures

			window.LatestState = "blocked"

			continue
		}

		if !harnessAuditContextReportIsFresh(ctx) {
			continue
		}

		if window.UnresolvedBlockers > 0 || window.UnresolvedStaleUse > 0 || window.UnresolvedRefreshFailures > 0 {
			window.FreshResultsAfterBlocker++
		}

		window.ResolvedBlockers += window.UnresolvedBlockers
		window.ResolvedStaleUse += window.UnresolvedStaleUse
		window.ResolvedRefreshFailures += window.UnresolvedRefreshFailures

		window.UnresolvedBlockers = 0
		window.UnresolvedStaleUse = 0
		window.UnresolvedRefreshFailures = 0
		window.LatestState = "fresh"
	}

	if window.LatestState == "" && window.ResolvedBlockers == 0 && window.UnresolvedBlockers == 0 &&
		window.ResolvedStaleUse == 0 && window.UnresolvedStaleUse == 0 &&
		window.ResolvedRefreshFailures == 0 && window.UnresolvedRefreshFailures == 0 {

		return nil
	}

	return &window
}

func harnessAuditContextReportIsFresh(ctx *eval.ContextStats) bool {
	if ctx == nil {
		return false
	}

	return ctx.FreshnessReadThroughResults > 0 &&
		(ctx.FreshResults > 0 || ctx.StaleUseFresh > 0 || ctx.AutoRefreshFreshAfter > 0)
}

func harnessAuditPerfReportTime(root string, report eval.PerfReport) (time.Time, bool) {
	path := report.LogPath
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}

	if parsed, ok := harnessAuditLogFirstTimestamp(root, path); ok {
		return parsed, true
	}

	for _, value := range []string{
		report.SessionID,
		strings.TrimSuffix(filepath.Base(report.LogPath), filepath.Ext(report.LogPath)),
	} {
		if parsed, ok := harnessAuditTimestampFromLabel(value); ok {
			return parsed, true
		}
	}

	file, err := safepath.OpenFileUnderRoot(root, path, "audit log", safepath.Options{})
	if err == nil {
		defer file.Close()
		if info, statErr := file.Stat(); statErr == nil {
			return info.ModTime().UTC(), true
		}
	}

	return time.Time{}, false
}

func harnessAuditLogFirstTimestamp(root string, path string) (time.Time, bool) {
	if strings.TrimSpace(path) == "" {
		return time.Time{}, false
	}

	file, err := safepath.OpenFileUnderRoot(root, path, "audit log", safepath.Options{})
	if err != nil {
		return time.Time{}, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lines := 0; scanner.Scan() && lines < 25; lines++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]json.RawMessage

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		if parsed, ok := harnessAuditTimestampFromRaw(record["ts"]); ok {
			return parsed, true
		}

		if parsed, ok := harnessAuditTimestampFromRaw(record["timestamp"]); ok {
			return parsed, true
		}
	}

	return time.Time{}, false
}

func harnessAuditTimestampFromRaw(raw json.RawMessage) (time.Time, bool) {
	var value string

	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return time.Time{}, false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}

	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}

func historicalLogBehaviorFindings(aggregate eval.PerfAggregate, currentAggregate eval.PerfAggregate) []harnessAuditFinding {
	if len(aggregate.PatternHitRate) == 0 || len(currentAggregate.PatternHitRate) > 0 {
		return nil
	}

	var (
		patternCount int
		totalHits    int
	)

	for _, pattern := range aggregate.PatternHitRate {
		if pattern.LogsHit == 0 {
			continue
		}

		patternCount++

		totalHits += pattern.TotalCount
	}

	if patternCount == 0 {
		return nil
	}

	return []harnessAuditFinding{{
		ID:             "historical_behavior_patterns",
		Severity:       "info",
		Evidence:       fmt.Sprintf("%d pre-current behavior pattern(s), %d total hit(s), retained in log_behavior.aggregate", patternCount, totalHits),
		Recommendation: "Current behavior findings are generated only from post-cutoff logs; historical patterns remain visible in aggregate/top_patterns for trend analysis.",
		Tools:          []string{"harness_audit"},
	}}
}

func logBehaviorQuestions(aggregate eval.PerfAggregate) []harnessAuditQuestion {
	if aggregate.LogCount == 0 {
		return []harnessAuditQuestion{{
			Area:      "behavior_logs",
			Question:  "Which behavior logs should be captured to make harness improvement evidence-based?",
			Evidence:  "No JSONL logs were scanned.",
			Candidate: "Capture representative tasks with JSONL logging, then inspect the behavior-audit signal again.",
		}}
	}

	var questions []harnessAuditQuestion

	if hit := auditPatternHit(aggregate, "zero_hit_search"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_tooling",
			Question:  "Do repeated zero-hit searches point to missing query normalization, better symbol fallback, or a dedicated question tool?",
			Evidence:  fmt.Sprintf("zero_hit_search hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Cluster the raw failed patterns before adding a new tool; if they share shape, add one bounded deterministic probe.",
		})
	}

	if hit := auditPatternHit(aggregate, "dir_walk_streak"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_behavior",
			Question:  "Should broad directory walking surface a discovery-packet or language-overview capability earlier?",
			Evidence:  fmt.Sprintf("dir_walk_streak hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Prefer nudges and manifest shaping before hard-blocking safe reads.",
		})
	}

	if hit := auditPatternHit(aggregate, "rejection_retry"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "control_rails",
			Question:  "Are rejection packets clear enough, or should the harness hide the rejected tool/shape sooner?",
			Evidence:  fmt.Sprintf("rejection_retry hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Keep at least one safe model-directed path open; hide only repeated wasteful shapes.",
		})
	}

	if hit := auditPatternHit(aggregate, "malformed_edit_args"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "tool_schema",
			Question:  "Do write tools need stricter schemas, clearer examples, or host-side argument repair?",
			Evidence:  fmt.Sprintf("malformed_edit_args hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Tighten schema/examples first; add repair only for unambiguous transformations.",
		})
	}

	if hit := auditPatternHit(aggregate, "completion_gate_reopened_discovery"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "completion_recovery",
			Question:  "Did completion-gate recovery leave read/discovery looking like the normal next step after a required write was missing?",
			Evidence:  fmt.Sprintf("completion_gate_reopened_discovery hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Expose the recovery contract outcomes explicitly: required-artifact write, recorded uncertainty, or concrete blocker.",
		})
	}

	if hit := auditPatternHit(aggregate, "required_paths_blocked_without_write"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "write_contract",
			Question:  "Which route or recovery packet allowed an implementation run to end before any required-path write attempt?",
			Evidence:  fmt.Sprintf("required_paths_blocked_without_write hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Inspect the preceding completion_gate_recovery and next_action_state events before changing prompts; this is usually a contract-surface issue.",
		})
	}

	if hit := auditPatternHit(aggregate, "named_gap_read_walk"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "evidence_closure",
			Question:  "Which named edit-safety gap stayed open across repeated reads instead of becoming a plan, a write, or a blocker?",
			Evidence:  fmt.Sprintf("named_gap_read_walk hit %d read(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Audit whether the packet already contained the answer, whether a terminal/source-satisfied transition was missed, or whether the gap needs a deterministic closeout affordance.",
		})
	}

	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "agent_feedback",
			Question:  "Which reported idiom harness/tool/prompt gap should be fixed before adding more task-specific guidance?",
			Evidence:  fmt.Sprintf("%d feedback event(s); top category %s; top component %s", aggregate.Feedback.Total, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Candidate: "Cluster by component and fix the smallest generic contract that removes the workaround.",
		})
	}

	if hit := auditPatternHit(aggregate, "prompt_surface_overlap"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "prompt_surface",
			Question:  "Is degraded behavior coming from overlapping prompt ownership rather than missing guidance?",
			Evidence:  fmt.Sprintf("prompt_surface_overlap hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Move repeated obligations into typed runtime briefs, deterministic checks, or one canonical prompt owner; delete duplicate prose before adding a new rule.",
		})
	}

	genericReads := auditToolCalls(aggregate, ToolNameListFiles, ToolNameSearchFiles, ToolNameSearchContent, ToolNameReadFile)
	languageReads := auditToolPrefixCalls(aggregate, "go_", "cpp_", "python_", "ts_", "rust_", "java_")

	if genericReads >= 10 && genericReads > languageReads*2 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "language_tool_selection",
			Question:  "Are agents missing structured language-aware routes, or are those routes failing and causing fallback to generic reads?",
			Evidence:  fmt.Sprintf("generic read/search calls=%d, language-aware route calls=%d", genericReads, languageReads),
			Candidate: "Sample logs with high generic-read share and inspect precision_tier/unavailable results before adding tools.",
		})
	}

	return questions
}

func firstAuditPatterns(in []eval.PerfPatternHit, n int) []eval.PerfPatternHit {
	if len(in) <= n {
		return append([]eval.PerfPatternHit(nil), in...)
	}

	return append([]eval.PerfPatternHit(nil), in[:n]...)
}

func firstAuditTools(in []eval.PerfToolMix, n int) []eval.PerfToolMix {
	if len(in) <= n {
		return append([]eval.PerfToolMix(nil), in...)
	}

	return append([]eval.PerfToolMix(nil), in[:n]...)
}

func auditObservedCapabilityMix(root string, paths []string, logCount int, warnings *[]string, displayPath func(string) string) []harnessAuditCapabilityMix {
	callsByCapability := make(map[string]int)
	total := 0

	for _, path := range paths {
		file, err := safepath.OpenFileUnderRoot(root, path, "audit log", safepath.Options{})
		var records []eval.Record
		if err == nil {
			records, err = eval.LoadRecordsFromReader(file)
			closeErr := file.Close()
			if err == nil && closeErr != nil {
				err = closeErr
			}
		}
		if err != nil {
			if warnings != nil {
				label := path
				if displayPath != nil {
					label = displayPath(path)
				}

				*warnings = append(*warnings, fmt.Sprintf("%s: capability mix scan: %v", label, err))
			}

			continue
		}

		for _, record := range records {
			if record.Type != "tool_request" {
				continue
			}

			for _, capability := range auditObservedCapabilitiesForToolRequest(record) {
				callsByCapability[capability]++

				total++
			}
		}
	}

	if len(callsByCapability) == 0 {
		return nil
	}

	out := make([]harnessAuditCapabilityMix, 0, len(callsByCapability))
	for capability, calls := range callsByCapability {
		row := harnessAuditCapabilityMix{
			Capability: capability,
			Calls:      calls,
		}
		if total > 0 {
			row.Share = float64(calls) / float64(total)
		}

		if logCount > 0 {
			row.AvgPerLog = float64(calls) / float64(logCount)
		}

		out = append(out, row)
	}

	slices.SortFunc(out, func(a, b harnessAuditCapabilityMix) int {
		if c := cmp.Compare(b.Calls, a.Calls); c != 0 {
			return c
		}

		return cmp.Compare(a.Capability, b.Capability)
	})

	return out
}

func auditObservedCapabilitiesForToolRequest(record eval.Record) []string {
	tool := auditRecordString(record.Data, "tool")
	raw := auditRecordString(record.Data, "raw")
	rawFields := auditRawToolRequestFields(raw)
	capability := auditRecordString(record.Data, "capability")

	if capability == "" {
		capability = auditRecordString(rawFields, "capability")
	}

	if capability == "" {
		capability = auditRecordString(rawFields, "operation")
	}

	if capability == "" {
		capability = auditRecordString(rawFields, "resolution")
	}

	if capability != "" {
		if mapped := auditObservedCapabilityForToolCapability(tool, capability); mapped != "" {
			return []string{mapped}
		}
	}

	if tool == ToolNameReadOnlyCapability {
		for _, key := range []string{"id", "action", "query"} {
			if mapped := auditObservedCapabilityForToolCapability(tool, auditRecordString(rawFields, key)); mapped != "" {
				return []string{mapped}
			}
		}
	}

	static := EvidenceCapabilitiesForTool(tool)
	if len(static) == 1 && strings.TrimSpace(static[0]) != "" {
		return []string{strings.TrimSpace(static[0])}
	}

	return nil
}

func auditRawToolRequestFields(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	fields := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil
	}

	return fields
}

func auditRecordString(data map[string]any, key string) string {
	if len(data) == 0 {
		return ""
	}

	switch value := data[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func auditObservedCapabilityForToolCapability(tool, capability string) string {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return ""
	}

	if tool == ToolNameReadOnlyCapability {
		return ReadOnlyCapabilityEvidenceCapability(capability)
	}

	if mapped := evidenceCapabilityForLanguageCapability(semanticlanguage.Capability(capability)); mapped != "" {
		return mapped
	}

	switch strings.ReplaceAll(strings.ToLower(capability), "-", "_") {
	case EvidenceCapabilityInspectFile, EvidenceCapabilityInspectSymbol, EvidenceCapabilityFindDefinition, EvidenceCapabilityFindReferences,
		EvidenceCapabilityCallsFrom, EvidenceCapabilityCallsInto, EvidenceCapabilityRelatedFiles, EvidenceCapabilityQueryContext,
		EvidenceCapabilitySourceDiagnostics, EvidenceCapabilityVerifyEdit, EvidenceCapabilityHistoryContext, EvidenceCapabilitySecurityScan:
		return strings.ReplaceAll(strings.ToLower(capability), "-", "_")
	}

	return ""
}

func firstAuditCapabilities(in []harnessAuditCapabilityMix, n int) []harnessAuditCapabilityMix {
	if len(in) <= n {
		return append([]harnessAuditCapabilityMix(nil), in...)
	}

	return append([]harnessAuditCapabilityMix(nil), in[:n]...)
}

func auditPatternHit(aggregate eval.PerfAggregate, name string) eval.PerfPatternHit {
	for _, hit := range aggregate.PatternHitRate {
		if hit.Name == name {
			return hit
		}
	}

	return eval.PerfPatternHit{Name: name}
}

func auditToolCalls(aggregate eval.PerfAggregate, names ...string) int {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}

	total := 0

	for _, tool := range aggregate.ToolMix {
		if _, ok := want[tool.Tool]; ok {
			total += tool.Calls
		}
	}

	return total
}

func auditToolPrefixCalls(aggregate eval.PerfAggregate, prefixes ...string) int {
	total := 0

	for _, tool := range aggregate.ToolMix {
		for _, prefix := range prefixes {
			if strings.HasPrefix(tool.Tool, prefix) {
				total += tool.Calls
				break
			}
		}
	}

	return total
}
