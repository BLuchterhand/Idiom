package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
)

var harnessAuditGateMetadataIntroducedAt = time.Date(2026, 5, 24, 23, 56, 18, 0, time.UTC)
var harnessAuditTurnPhaseProjectionIntroducedAt = time.Date(2026, 6, 11, 22, 8, 0, 0, time.UTC)
var harnessAuditContractCoverageIntroducedAt = time.Date(2026, 6, 18, 13, 55, 0, 0, time.UTC)
var harnessAuditOpenEvidenceFinalAnswerProjectionFixedAt = time.Date(2026, 6, 22, 18, 25, 0, 0, time.UTC)

func harnessAuditGuardFrictionCoverage(report *autonomy.EventAuditReport) *autonomy.GuardFrictionCoverage {
	if report == nil || report.GuardFrictionCoverage.Total == 0 {
		return nil
	}

	coverage := report.GuardFrictionCoverage

	return &coverage
}

func (m *Manager) auditCurrentContractEventCoverage(paths []string) (*autonomy.EventAuditReport, []string) {
	return m.auditContractEventCoverageWithFilter(paths, currentHarnessAuditAutonomyEvents)
}

func (m *Manager) auditContractEventCoverageWithFilter(paths []string, filter func([]autonomy.Event) []autonomy.Event) (*autonomy.EventAuditReport, []string) {
	var (
		warnings []string
		events   []autonomy.Event
	)

	for _, path := range paths {
		pathEvents, pathWarnings := readHarnessAuditAutonomyEventsWithOptions(path, harnessAuditAutonomyDecodeOptions{})
		includeWarnings := filter == nil || m.harnessAuditPathIsCurrent(path)
		if includeWarnings {
			for _, warning := range pathWarnings {
				warnings = append(warnings, fmt.Sprintf("%s contract coverage: %s", m.pathAccess.DisplayPath(path), warning))
			}
		}

		events = append(events, pathEvents...)
	}

	if len(events) == 0 {
		return nil, warnings
	}

	if filter != nil {
		events = filter(events)
	}
	events = dedupeAndSortHarnessAuditAutonomyEvents(events)

	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})

	return &report, warnings
}

func (m *Manager) harnessAuditPathIsCurrent(path string) bool {
	if parsed, ok := harnessAuditLogFirstTimestamp(path); ok {
		return !parsed.Before(harnessAuditContractCoverageIntroducedAt)
	}
	for _, value := range []string{
		filepath.Base(path),
		strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
	} {
		if parsed, ok := harnessAuditTimestampFromLabel(value); ok {
			return !parsed.Before(harnessAuditContractCoverageIntroducedAt)
		}
	}
	if info, err := os.Stat(path); err == nil {
		return !info.ModTime().UTC().Before(harnessAuditContractCoverageIntroducedAt)
	}

	return true
}

func currentHarnessAuditAutonomyEvents(events []autonomy.Event) []autonomy.Event {
	current := make([]autonomy.Event, 0, len(events))
	for _, event := range events {
		if preFixOpenEvidenceFinalAnswerProjectionEvent(event) {
			continue
		}
		if event.OccurredAt.IsZero() || !event.OccurredAt.Before(harnessAuditContractCoverageIntroducedAt) {
			current = append(current, event)
		}
	}

	return current
}

func preFixOpenEvidenceFinalAnswerProjectionEvent(event autonomy.Event) bool {
	if event.OccurredAt.IsZero() || !event.OccurredAt.Before(harnessAuditOpenEvidenceFinalAnswerProjectionFixedAt) {
		return false
	}
	if event.Lifecycle == nil || event.Lifecycle.TurnPhaseContract == nil {
		return false
	}

	phase := event.Lifecycle.TurnPhaseContract
	return slices.Contains(phase.ActionPolicy.AllowedActionClasses, string(autonomy.ActionClassFinalAnswer)) &&
		autonomy.PhaseEvidenceStateHasOpenObligation(phase.EvidenceState)
}

func dedupeAndSortHarnessAuditAutonomyEvents(events []autonomy.Event) []autonomy.Event {
	if len(events) < 2 {
		return events
	}

	out := make([]autonomy.Event, 0, len(events))
	seen := make(map[string]struct{}, len(events))

	for _, event := range events {
		eventID := strings.TrimSpace(event.EventID)
		if eventID != "" {
			if _, ok := seen[eventID]; ok {
				continue
			}

			seen[eventID] = struct{}{}
		}

		out = append(out, event)
	}

	slices.SortStableFunc(out, func(a, b autonomy.Event) int {
		if !a.OccurredAt.Equal(b.OccurredAt) {
			if a.OccurredAt.IsZero() {
				return 1
			}

			if b.OccurredAt.IsZero() {
				return -1
			}

			if a.OccurredAt.Before(b.OccurredAt) {
				return -1
			}

			return 1
		}

		if cmp := strings.Compare(a.ContractID, b.ContractID); cmp != 0 {
			return cmp
		}

		if a.Sequence < b.Sequence {
			return -1
		}

		if a.Sequence > b.Sequence {
			return 1
		}

		if cmp := strings.Compare(string(a.Type), string(b.Type)); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.EventID, b.EventID)
	})

	return out
}

type harnessAuditAutonomyDecodeOptions struct {
	PreserveLegacyTurnPhaseProjection bool
}

func readHarnessAuditAutonomyEventsWithOptions(path string, opts harnessAuditAutonomyDecodeOptions) ([]autonomy.Event, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{err.Error()}
	}

	var (
		events   []autonomy.Event
		warnings []string
	)

	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lineEvents, lineWarnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(line), opts)
		for _, warning := range lineWarnings {
			warnings = append(warnings, fmt.Sprintf("line %d: %s", lineNumber+1, warning))
		}

		events = append(events, lineEvents...)
	}

	return events, warnings
}

func decodeHarnessAuditAutonomyEventsWithOptions(line []byte, opts harnessAuditAutonomyDecodeOptions) ([]autonomy.Event, []string) {
	var (
		events   []autonomy.Event
		warnings []string
	)

	if auditRawAutonomyEvent(line) {
		event, err := unmarshalHarnessAuditAutonomyEventWithOptions(line, opts)
		if err != nil {
			warnings = append(warnings, err.Error())
		} else {
			events = append(events, event)
		}
	}

	var record map[string]json.RawMessage

	if err := json.Unmarshal(line, &record); err != nil {
		return events, warnings
	}

	for _, key := range []string{"autonomy_event", "contract_event", "event"} {
		raw, ok := record[key]
		if !ok || !auditRawAutonomyEvent(raw) {
			continue
		}

		event, err := unmarshalHarnessAuditAutonomyEventWithOptions(raw, opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", key, err))
			continue
		}

		events = append(events, event)
	}

	if auditStringFromRaw(record["role"]) == "autonomy_event" {
		var content string

		if err := json.Unmarshal(record["content"], &content); err == nil && auditRawAutonomyEvent([]byte(content)) {
			event, err := unmarshalHarnessAuditAutonomyEventWithOptions([]byte(content), opts)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("content: %v", err))
			} else {
				events = append(events, event)
			}
		}
	}

	return events, warnings
}

func auditRawAutonomyEvent(raw []byte) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}

	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}

	return probe.SchemaVersion == autonomy.EventSchemaVersion
}

func auditStringFromRaw(raw json.RawMessage) string {
	var value string

	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}

	return strings.TrimSpace(value)
}

func unmarshalHarnessAuditAutonomyEventWithOptions(raw []byte, opts harnessAuditAutonomyDecodeOptions) (autonomy.Event, error) {
	var event autonomy.Event

	if err := json.Unmarshal(raw, &event); err != nil {
		return autonomy.Event{}, err
	}

	normalizeHarnessAuditLegacyGateFields(raw, &event)

	if !opts.PreserveLegacyTurnPhaseProjection {
		normalizeHarnessAuditLegacyTurnPhaseProjection(&event)
	}

	event.Normalize()

	if err := event.Validate(); err != nil {
		return autonomy.Event{}, err
	}

	return event, nil
}

func normalizeHarnessAuditLegacyTurnPhaseProjection(event *autonomy.Event) {
	if event == nil || event.Lifecycle == nil || !event.OccurredAt.Before(harnessAuditTurnPhaseProjectionIntroducedAt) {
		return
	}

	if event.Lifecycle.TurnPhaseContract == nil &&
		event.Lifecycle.ModelProtocol == nil &&
		len(event.Lifecycle.SurfaceSummaries) == 0 {

		return
	}

	event.Lifecycle.TurnPhaseContract = nil
	event.Lifecycle.ModelProtocol = nil
	event.Lifecycle.SurfaceSummaries = nil
}

func normalizeHarnessAuditLegacyGateFields(raw []byte, event *autonomy.Event) {
	if event == nil || event.Type != autonomy.EventGateDecision || event.Gate == nil {
		return
	}

	if !event.OccurredAt.Before(harnessAuditGateMetadataIntroducedAt) {
		return
	}

	var record map[string]json.RawMessage

	if err := json.Unmarshal(raw, &record); err != nil {
		return
	}

	var gateFields map[string]json.RawMessage

	if err := json.Unmarshal(record["gate"], &gateFields); err != nil {
		return
	}

	if _, ok := gateFields["pack_id"]; !ok && event.Gate.PackID == "" {
		event.Gate.PackID = harnessAuditLegacyGatePackID(event.Gate.Gate, event.Gate.InvariantID)
	}

	if _, ok := gateFields["enforcement_mode"]; !ok && event.Gate.EnforcementMode == "" {
		event.Gate.EnforcementMode = harnessAuditLegacyGateEnforcementMode(event.Gate.Decision)
	}

	if _, ok := gateFields["safe_alternatives"]; !ok &&
		harnessAuditLegacyGateNeedsSafeAlternatives(event.Gate.Decision, event.Gate.EnforcementMode) &&
		len(event.Gate.SafeAlternatives) == 0 {

		event.Gate.SafeAlternatives = harnessAuditLegacyGateSafeAlternatives(event.Gate.Gate)
	}
}

func harnessAuditLegacyGatePackID(gate autonomy.GateKind, invariantID string) autonomy.ContractPackID {
	switch strings.TrimSpace(invariantID) {
	case "completion.readiness":
		return autonomy.ContractPackCompletionReadiness
	case "write.required_edit_paths":
		return autonomy.ContractPackWriteRequiredPaths
	case "verification.post_write":
		return autonomy.ContractPackVerificationPostWrite
	case "loop.repeated_call":
		return autonomy.ContractPackLoopRepeatedCall
	case "budget.run_policy":
		return autonomy.ContractPackBudgetRunPolicy
	case "read_only.readiness":
		return autonomy.ContractPackCompletionReadiness
	}

	switch gate {
	case autonomy.GateCompletion:
		return autonomy.ContractPackCompletionReadiness
	case autonomy.GateWrite:
		return autonomy.ContractPackWriteRequiredPaths
	case autonomy.GateVerification:
		return autonomy.ContractPackVerificationPostWrite
	case autonomy.GateLoop:
		return autonomy.ContractPackLoopRepeatedCall
	case autonomy.GateBudget:
		return autonomy.ContractPackBudgetRunPolicy
	case autonomy.GateReadOnly:
		return autonomy.ContractPackCompletionReadiness
	default:
		return ""
	}
}

func harnessAuditLegacyGateEnforcementMode(decision autonomy.ToolPolicyDecision) autonomy.EnforcementMode {
	if decision == autonomy.PolicyObserve {
		return autonomy.EnforcementModeObserveOnly
	}

	return autonomy.EnforcementModeEnforce
}

func harnessAuditLegacyGateNeedsSafeAlternatives(decision autonomy.ToolPolicyDecision, mode autonomy.EnforcementMode) bool {
	if mode != autonomy.EnforcementModeEnforce {
		return false
	}

	switch decision {
	case autonomy.PolicyReject, autonomy.PolicyDeny, autonomy.PolicyStop:
		return true
	default:
		return false
	}
}

func harnessAuditLegacyGateSafeAlternatives(gate autonomy.GateKind) []string {
	switch gate {
	case autonomy.GateCompletion:
		return []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	case autonomy.GateWrite:
		return []string{"edit required_edit_paths", "<<QUESTION>>:"}
	case autonomy.GateVerification:
		return []string{"run verification", "report verification blocker"}
	case autonomy.GateLoop:
		return []string{"change tool target", "Decision: source-satisfied"}
	case autonomy.GateBudget:
		return []string{"Decision: blocker", "Decision: source-satisfied"}
	case autonomy.GateReadOnly:
		return []string{"read evidence paths", "Decision: source-satisfied"}
	default:
		return []string{"choose allowed action", "<<QUESTION>>:"}
	}
}

func historicalTurnPhaseProjectionFindings(paths []string) []harnessAuditFinding {
	var events []autonomy.Event

	for _, path := range paths {
		pathEvents, _ := readHarnessAuditAutonomyEventsWithOptions(path, harnessAuditAutonomyDecodeOptions{
			PreserveLegacyTurnPhaseProjection: true,
		})

		events = append(events, pathEvents...)
	}

	return historicalTurnPhaseProjectionFindingsFromEvents(events)
}

func historicalTurnPhaseProjectionFindingsFromEvents(events []autonomy.Event) []harnessAuditFinding {
	historicalEvents := make([]autonomy.Event, 0, len(events))
	for _, event := range events {
		if !event.OccurredAt.Before(harnessAuditTurnPhaseProjectionIntroducedAt) || event.Lifecycle == nil {
			continue
		}

		if event.Lifecycle.TurnPhaseContract == nil &&
			event.Lifecycle.ModelProtocol == nil &&
			len(event.Lifecycle.SurfaceSummaries) == 0 {

			continue
		}

		historicalEvents = append(historicalEvents, event)
	}

	if len(historicalEvents) == 0 {
		return nil
	}

	report := autonomy.AuditEvents(dedupeAndSortHarnessAuditAutonomyEvents(historicalEvents), autonomy.EventAuditOptions{})
	contradictions := len(report.TurnPhaseCoverage.Contradictions)

	if contradictions == 0 {
		contradictions = legacyTurnPhaseProjectionContradictions(historicalEvents)
	}

	if contradictions == 0 {
		return nil
	}

	return []harnessAuditFinding{{
		ID:             "historical_turn_phase_contract_contradiction",
		Severity:       "info",
		Evidence:       fmt.Sprintf("%d pre-current turn-phase projection contradiction(s), retained outside current contract coverage", contradictions),
		Recommendation: "Historical projection mismatches are retained as compatibility evidence; post-cutoff turn-phase contradictions still raise turn_phase_contract_contradiction.",
		Tools:          []string{"harness_audit"},
	}}
}

func legacyTurnPhaseProjectionContradictions(events []autonomy.Event) int {
	count := 0

	for _, event := range events {
		if event.Lifecycle == nil || event.Lifecycle.TurnPhaseContract == nil {
			continue
		}

		phase := *event.Lifecycle.TurnPhaseContract
		if !strings.EqualFold(phase.RuntimeCall.ToolTransportMode, "textual") {
			continue
		}

		for _, tool := range phase.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" || stringSetContainsWorkspaceAudit(phase.RuntimeCall.ExpressibleActionClasses, tool) {
				continue
			}

			count++

			break
		}

		if count == 0 {
			for _, surface := range event.Lifecycle.SurfaceSummaries {
				if strings.EqualFold(surface.RuntimeCall.ToolTransportMode, "textual") && legacySurfaceSummaryHasUnexpressibleTool(surface) {
					count++
					break
				}
			}
		}
	}

	return count
}

func legacySurfaceSummaryHasUnexpressibleTool(surface autonomy.TurnPhaseSurfaceSummaryEvent) bool {
	for _, tool := range surface.CallableTools {
		tool = strings.TrimSpace(tool)
		if tool == "" || stringSetContainsWorkspaceAudit(surface.RuntimeCall.ExpressibleActionClasses, tool) {
			continue
		}

		return true
	}

	return false
}

func historicalOpenEvidenceFinalAnswerProjectionFindings(paths []string) []harnessAuditFinding {
	var count int

	for _, path := range paths {
		pathEvents, _ := readHarnessAuditAutonomyEventsWithOptions(path, harnessAuditAutonomyDecodeOptions{})
		for _, event := range pathEvents {
			if preFixOpenEvidenceFinalAnswerProjectionEvent(event) {
				count++
			}
		}
	}

	if count == 0 {
		return nil
	}

	return []harnessAuditFinding{{
		ID:             "historical_open_evidence_final_answer_projection",
		Severity:       "info",
		Evidence:       fmt.Sprintf("%d pre-fix turn-phase projection(s) allowed final_answer while open evidence obligations remained", count),
		Recommendation: "Pre-fix open-evidence/final-answer projection mismatches are retained as historical evidence; post-fix contradictions still raise turn_phase_contract_contradiction.",
		Tools:          []string{"harness_audit"},
	}}
}

func stringSetContainsWorkspaceAudit(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}

	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}

func historicalContractCoverageWarningFindings(allWarnings []string, currentWarnings []string) []harnessAuditFinding {
	historicalInvalidEvents := invalidContractWarningCount(allWarnings) - invalidContractWarningCount(currentWarnings)
	if historicalInvalidEvents <= 0 {
		return nil
	}

	return []harnessAuditFinding{{
		ID:             "historical_contract_event_invalid",
		Severity:       "info",
		Evidence:       fmt.Sprintf("%d pre-current invalid autonomy event(s) skipped during contract coverage audit", historicalInvalidEvents),
		Recommendation: "Historical invalid events are retained in log_behavior.warnings; current invalid events still raise contract_event_invalid.",
		Tools:          []string{"harness_audit"},
	}}
}

func contractCoverageWarningFindingsWithID(id string, warnings []string) []harnessAuditFinding {
	invalidEvents := invalidContractWarningCount(warnings)
	if invalidEvents == 0 {
		return nil
	}

	return []harnessAuditFinding{{
		ID:             id,
		Severity:       "medium",
		Evidence:       fmt.Sprintf("%d invalid autonomy event(s) skipped during contract coverage audit", invalidEvents),
		Recommendation: "Fix the event emitter or migration path before using contract coverage as release evidence.",
		Tools:          []string{"harness_audit"},
	}}
}

func invalidContractWarningCount(warnings []string) int {
	invalidEvents := 0

	for _, warning := range warnings {
		if strings.Contains(warning, "invalid autonomy event") {
			invalidEvents++
		}
	}

	return invalidEvents
}

func contractCoverageFindings(report *autonomy.EventAuditReport) []harnessAuditFinding {
	if report == nil {
		return nil
	}

	findings := make([]harnessAuditFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		findings = append(findings, harnessAuditFinding{
			ID:             finding.ID,
			Severity:       finding.Severity,
			Evidence:       finding.Evidence,
			Recommendation: contractCoverageFindingRecommendation(finding),
			Tools:          []string{"harness_audit"},
			EventIDs:       boundedAuditIdentifiers(finding.EventIDs, 8),
			ContractIDs:    boundedAuditIdentifiers(finding.ContractIDs, 8),
			DetailCount:    finding.DetailCount,
			Details:        boundedAuditFindingDetails(finding.Details, 8),
		})
	}

	return findings
}

func boundedAuditFindingDetails(details []autonomy.EventAuditFindingDetail, limit int) []autonomy.EventAuditFindingDetail {
	if limit <= 0 || len(details) == 0 {
		return nil
	}

	out := append([]autonomy.EventAuditFindingDetail(nil), details...)
	if len(out) > limit {
		out = out[:limit]
	}

	return out
}

func contractCoverageFindingRecommendation(finding autonomy.EventAuditFinding) string {
	parts := []string{"inspect contract_coverage.findings for full non-raw refs"}

	if ids := boundedAuditIdentifiers(finding.EventIDs, 3); len(ids) > 0 {
		parts = append(parts, "event_ids="+strings.Join(ids, ","))
	}

	if ids := boundedAuditIdentifiers(finding.ContractIDs, 3); len(ids) > 0 {
		parts = append(parts, "contract_ids="+strings.Join(ids, ","))
	}

	switch finding.ID {
	case "turn_phase_contract_contradiction":
		parts = append(parts, "fix the phase projection or tool policy emitter named by those refs")
	case "contract_event_privacy_forbidden_raw_content":
		parts = append(parts, "redact or remove raw-content metadata at the emitting boundary named by those refs")
	}

	return strings.Join(parts, "; ")
}

func boundedAuditIdentifiers(ids []string, limit int) []string {
	if limit <= 0 || len(ids) == 0 {
		return nil
	}

	uniq := textutil.Sorted(ids)
	if len(uniq) > limit {
		uniq = uniq[:limit]
	}

	return uniq
}

func splitHistoricalHarnessAuditFindings(findings []harnessAuditFinding) ([]harnessAuditFinding, []harnessAuditFinding) {
	var (
		current    []harnessAuditFinding
		historical []harnessAuditFinding
	)

	for _, finding := range findings {
		if harnessAuditFindingIsHistorical(finding) {
			historical = append(historical, finding)
			continue
		}

		current = append(current, finding)
	}

	return current, historical
}

func harnessAuditFindingIsHistorical(finding harnessAuditFinding) bool {
	id := strings.TrimSpace(finding.ID)

	return strings.HasPrefix(id, "historical_") ||
		id == "context_cache_historical_stale_rows" ||
		id == "context_cache_historical_unresolved_gaps"
}
