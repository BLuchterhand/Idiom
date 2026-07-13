package workspace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	intentloopcontract "idiom/internal/contracts/intentloop"
)

type harnessAuditIntentLoop struct {
	LogsHit              int            `json:"logs_hit,omitempty"`
	Sessions             int            `json:"sessions,omitempty"`
	Starts               int            `json:"starts,omitempty"`
	CycleStarts          int            `json:"cycle_starts,omitempty"`
	CycleCompletes       int            `json:"cycle_completes,omitempty"`
	Completions          int            `json:"completions,omitempty"`
	Blocked              int            `json:"blocked,omitempty"`
	NoProgress           int            `json:"no_progress,omitempty"`
	BudgetExhausted      int            `json:"budget_exhausted,omitempty"`
	BudgetWarnings       int            `json:"budget_warnings,omitempty"`
	TotalReportedCycles  int            `json:"total_reported_cycles,omitempty"`
	TotalReportedGaps    int            `json:"total_reported_gaps,omitempty"`
	EventCounts          map[string]int `json:"event_counts,omitempty"`
	GapRequirementCounts map[string]int `json:"gap_requirement_counts,omitempty"`
	GapStatusCounts      map[string]int `json:"gap_status_counts,omitempty"`
}

type harnessAuditFeaturePlanner struct {
	LogsHit                 int               `json:"logs_hit,omitempty"`
	EventCounts             map[string]int    `json:"event_counts,omitempty"`
	ArtifactIDs             []string          `json:"artifact_ids,omitempty"`
	PlanRevisions           []int             `json:"plan_revisions,omitempty"`
	PlanDigests             []string          `json:"plan_digests,omitempty"`
	WorkspaceFingerprints   []string          `json:"workspace_fingerprints,omitempty"`
	ApprovalEvents          []string          `json:"approval_events,omitempty"`
	VerificationClosures    []string          `json:"verification_closures,omitempty"`
	VerificationStatuses    map[string]int    `json:"verification_statuses,omitempty"`
	ExecutionHandoffEvents  int               `json:"execution_handoff_events,omitempty"`
	OpenQuestionEvents      int               `json:"open_question_events,omitempty"`
	QuestionAnswerEvents    int               `json:"question_answer_events,omitempty"`
	PlanRejectedEvents      int               `json:"plan_rejected_events,omitempty"`
	CoverageAuthorityBlocks int               `json:"coverage_authority_blocks,omitempty"`
	PlanApprovedEvents      int               `json:"plan_approved_events,omitempty"`
	AtomTerminalEvents      int               `json:"atom_terminal_events,omitempty"`
	ReleaseReadyEvents      int               `json:"release_ready_events,omitempty"`
	ReleaseBlockedEvents    int               `json:"release_blocked_events,omitempty"`
	MissingArtifactBindings []harnessAuditRef `json:"missing_artifact_bindings,omitempty"`
	CorrelationFailures     []harnessAuditRef `json:"correlation_failures,omitempty"`
	terminalClosures        map[string]featurePlannerAuditBinding
	releaseDecisions        []featurePlannerAuditRelease
}

type harnessAuditRef struct {
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Problem string `json:"problem,omitempty"`
}

type featurePlannerAuditBinding struct {
	EventType            string
	Path                 string
	Line                 int
	ArtifactID           string
	PlanRevision         int
	PlanDigest           string
	WorkspaceFingerprint string
	ApprovalEventID      string
}

type featurePlannerAuditRelease struct {
	Binding    featurePlannerAuditBinding
	ClosureIDs []string
}

var intentLoopAuditEvents = intentLoopAuditEventSet()

func intentLoopAuditEventSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range intentloopcontract.EventNames() {
		out[name] = struct{}{}
	}
	return out
}

func auditIntentLoopEvents(paths []string) *harnessAuditIntentLoop {
	out := &harnessAuditIntentLoop{
		EventCounts:          map[string]int{},
		GapRequirementCounts: map[string]int{},
		GapStatusCounts:      map[string]int{},
	}
	logsHit := map[string]struct{}{}
	sessions := map[string]struct{}{}

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			for _, event := range auditIntentLoopEventCandidates([]byte(line)) {
				eventType := auditString(event["type"])
				if _, ok := intentLoopAuditEvents[eventType]; !ok {
					continue
				}

				logsHit[path] = struct{}{}
				if sessionID := auditString(event["session_id"]); sessionID != "" {
					sessions[sessionID] = struct{}{}
				}

				out.EventCounts[eventType]++
				auditIntentLoopEvent(out, eventType, event)
			}
		}
	}

	out.LogsHit = len(logsHit)
	if out.LogsHit == 0 {
		return nil
	}

	out.Sessions = len(sessions)
	pruneEmptyIntentLoopAuditCounts(out)
	return out
}

func auditIntentLoopEventCandidates(line []byte) []map[string]any {
	var row map[string]any
	if err := json.Unmarshal(line, &row); err != nil {
		return nil
	}

	candidates := make([]map[string]any, 0, 3)
	candidates = appendIntentLoopEventCandidate(candidates, row)

	for _, key := range []string{"event", "intent_loop_event"} {
		if nested, ok := row[key].(map[string]any); ok {
			candidates = appendIntentLoopEventCandidate(candidates, nested)
		}
	}

	if content := auditString(row["content"]); content != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err == nil {
			candidates = appendIntentLoopEventCandidate(candidates, payload)
		}
	}

	return candidates
}

func appendIntentLoopEventCandidate(candidates []map[string]any, event map[string]any) []map[string]any {
	if event == nil {
		return candidates
	}
	if _, ok := intentLoopAuditEvents[auditString(event["type"])]; !ok {
		return candidates
	}
	return append(candidates, event)
}

func auditIntentLoopEvent(out *harnessAuditIntentLoop, eventType string, event map[string]any) {
	switch eventType {
	case intentloopcontract.EventName(intentloopcontract.EventStart):
		out.Starts++
	case intentloopcontract.EventName(intentloopcontract.EventCycleStart):
		out.CycleStarts++
	case intentloopcontract.EventName(intentloopcontract.EventCycleComplete):
		out.CycleCompletes++
		out.TotalReportedGaps += auditIntField(event, "gap_count")
		auditIntentLoopGaps(out, event["remaining_gaps"])
	case intentloopcontract.EventName(intentloopcontract.EventConvergenceDetected):
		out.NoProgress++
	case intentloopcontract.EventName(intentloopcontract.EventCycleBudgetWarning):
		out.BudgetWarnings++
	case intentloopcontract.EventName(intentloopcontract.EventBudgetExhausted):
		out.BudgetExhausted++
	case intentloopcontract.EventName(intentloopcontract.EventComplete):
		out.Completions++
		out.TotalReportedCycles += auditIntField(event, "total_cycles")
	case intentloopcontract.EventName(intentloopcontract.EventBlocked):
		out.Blocked++
	}
}

var featurePlannerAuditEvents = featurePlannerAuditEventSet()

func featurePlannerAuditEventSet() map[string]struct{} {
	events := featureplannercontract.EventNames()
	out := make(map[string]struct{}, len(events))
	for _, eventName := range events {
		out[eventName] = struct{}{}
	}
	return out
}

func auditFeaturePlannerEvents(paths []string, displayPath func(string) string) *harnessAuditFeaturePlanner {
	out := &harnessAuditFeaturePlanner{
		EventCounts:          map[string]int{},
		VerificationStatuses: map[string]int{},
		terminalClosures:     map[string]featurePlannerAuditBinding{},
	}
	logsHit := map[string]struct{}{}

	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			for _, event := range auditFeaturePlannerEventCandidates([]byte(line)) {
				eventType := auditString(event["type"])
				if _, ok := featurePlannerAuditEvents[eventType]; !ok {
					continue
				}

				logsHit[path] = struct{}{}
				out.EventCounts[eventType]++
				auditFeaturePlannerEvent(out, eventType, event, displayPath(path), lineNumber)
			}
		}

		_ = file.Close()
	}

	out.LogsHit = len(logsHit)
	if out.LogsHit == 0 {
		return nil
	}

	out.ArtifactIDs = uniqueSortedHarnessAuditStrings(out.ArtifactIDs)
	out.PlanDigests = uniqueSortedHarnessAuditStrings(out.PlanDigests)
	out.WorkspaceFingerprints = uniqueSortedHarnessAuditStrings(out.WorkspaceFingerprints)
	out.ApprovalEvents = uniqueSortedHarnessAuditStrings(out.ApprovalEvents)
	out.VerificationClosures = uniqueSortedHarnessAuditStrings(out.VerificationClosures)
	out.PlanRevisions = uniqueSortedHarnessAuditInts(out.PlanRevisions)
	correlateFeaturePlannerReleaseEvents(out)
	out.terminalClosures = nil
	out.releaseDecisions = nil

	return out
}

func auditFeaturePlannerEventCandidates(line []byte) []map[string]any {
	var row map[string]any
	if err := json.Unmarshal(line, &row); err != nil {
		return nil
	}

	var candidates []map[string]any
	candidates = appendFeaturePlannerEventCandidate(candidates, row)
	for _, key := range []string{"event", "feature_planner_event"} {
		if nested, ok := row[key].(map[string]any); ok {
			candidates = appendFeaturePlannerEventCandidate(candidates, nested)
		}
	}
	if content := auditString(row["content"]); content != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err == nil {
			candidates = appendFeaturePlannerEventCandidate(candidates, payload)
		}
	}

	return candidates
}

func appendFeaturePlannerEventCandidate(candidates []map[string]any, event map[string]any) []map[string]any {
	if event == nil {
		return candidates
	}
	if _, ok := featurePlannerAuditEvents[auditString(event["type"])]; !ok {
		return candidates
	}

	return append(candidates, event)
}

func auditFeaturePlannerEvent(out *harnessAuditFeaturePlanner, eventType string, event map[string]any, path string, line int) {
	fields, _ := event["fields"].(map[string]any)
	artifactID := firstNonEmptyHarnessAuditString(auditString(fields["plan_artifact_id"]), auditString(event["plan_artifact_id"]))
	planDigest := firstNonEmptyHarnessAuditString(auditString(fields["plan_digest"]), auditString(event["plan_digest"]))
	workspaceFingerprint := firstNonEmptyHarnessAuditString(auditString(fields["workspace_fingerprint"]), auditString(event["workspace_fingerprint"]))
	approvalEvent := firstNonEmptyHarnessAuditString(auditString(fields["approval_event_id"]), auditString(event["approval_event_id"]))
	closureID := firstNonEmptyHarnessAuditString(auditString(fields["verification_closure_event_id"]), auditString(event["verification_closure_event_id"]))
	planRevision := auditIntField(fields, "plan_revision")
	binding := featurePlannerAuditBinding{
		EventType:            eventType,
		Path:                 path,
		Line:                 line,
		ArtifactID:           artifactID,
		PlanRevision:         planRevision,
		PlanDigest:           planDigest,
		WorkspaceFingerprint: workspaceFingerprint,
		ApprovalEventID:      approvalEvent,
	}

	if artifactID != "" {
		out.ArtifactIDs = append(out.ArtifactIDs, artifactID)
	}
	if planDigest != "" {
		out.PlanDigests = append(out.PlanDigests, planDigest)
	}
	if workspaceFingerprint != "" {
		out.WorkspaceFingerprints = append(out.WorkspaceFingerprints, workspaceFingerprint)
	}
	if approvalEvent != "" {
		out.ApprovalEvents = append(out.ApprovalEvents, approvalEvent)
	}
	if planRevision > 0 {
		out.PlanRevisions = append(out.PlanRevisions, planRevision)
	}
	if closureID != "" {
		out.VerificationClosures = append(out.VerificationClosures, closureID)
	}
	out.VerificationClosures = append(out.VerificationClosures, auditStringList(fields["verification_closure_event_ids"])...)
	if status := auditString(fields["verification_status"]); status != "" {
		out.VerificationStatuses[status]++
	}

	switch eventType {
	case featureplannercontract.EventName(featureplannercontract.EventPlanApproved):
		out.PlanApprovedEvents++
		if artifactID == "" || planDigest == "" || workspaceFingerprint == "" {
			out.MissingArtifactBindings = append(out.MissingArtifactBindings, harnessAuditRef{Type: eventType, Path: path, Line: line, Problem: "plan approval event missing artifact revision, digest, or workspace fingerprint"})
		}
	case featureplannercontract.EventName(featureplannercontract.EventPlanAccepted), featureplannercontract.EventName(featureplannercontract.EventPlanReviewed):
		if artifactID == "" || planDigest == "" || workspaceFingerprint == "" {
			out.MissingArtifactBindings = append(out.MissingArtifactBindings, harnessAuditRef{Type: eventType, Path: path, Line: line, Problem: "plan lifecycle event missing artifact revision, digest, or workspace fingerprint"})
		}
	case featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionOpened), featureplannercontract.EventName(featureplannercontract.EventImplementorQuestionOpened):
		out.OpenQuestionEvents++
	case featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswered), featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionSupersededByRevision):
		out.QuestionAnswerEvents++
	case featureplannercontract.EventName(featureplannercontract.EventTDDPreExecutionBlocked), featureplannercontract.EventName(featureplannercontract.EventPlanExecuteCompletionRejected), featureplannercontract.EventName(featureplannercontract.EventStandaloneContextBlocked), featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), featureplannercontract.EventName(featureplannercontract.EventPlanContractBlocked):
		out.PlanRejectedEvents++
		if featurePlannerEventIndicatesCoverageAuthorityBlock(fields, event) {
			out.CoverageAuthorityBlocks++
		}
	case featureplannercontract.EventName(featureplannercontract.EventAtomDispatched):
		out.ExecutionHandoffEvents++
	case featureplannercontract.EventName(featureplannercontract.EventAtomCompleted), featureplannercontract.EventName(featureplannercontract.EventAtomSourceSatisfied):
		out.AtomTerminalEvents++
		if closureID == "" {
			out.MissingArtifactBindings = append(out.MissingArtifactBindings, harnessAuditRef{Type: eventType, Path: path, Line: line, Problem: "terminal atom event missing verification closure"})
		} else {
			out.terminalClosures[closureID] = binding
		}
	case featureplannercontract.EventName(featureplannercontract.EventReleaseReady):
		out.ReleaseReadyEvents++
		closureIDs := auditStringList(fields["verification_closure_event_ids"])
		if artifactID == "" || planDigest == "" || workspaceFingerprint == "" || approvalEvent == "" || len(closureIDs) == 0 {
			out.MissingArtifactBindings = append(out.MissingArtifactBindings, harnessAuditRef{Type: eventType, Path: path, Line: line, Problem: "release-ready event missing artifact, approval, or verification closure bindings"})
		}
		out.releaseDecisions = append(out.releaseDecisions, featurePlannerAuditRelease{Binding: binding, ClosureIDs: closureIDs})
	case featureplannercontract.EventName(featureplannercontract.EventReleaseBlocked):
		out.ReleaseBlockedEvents++
		if artifactID == "" || planDigest == "" || workspaceFingerprint == "" {
			out.MissingArtifactBindings = append(out.MissingArtifactBindings, harnessAuditRef{Type: eventType, Path: path, Line: line, Problem: "release-blocked event missing artifact binding"})
		}
	}
}

func correlateFeaturePlannerReleaseEvents(out *harnessAuditFeaturePlanner) {
	if out == nil {
		return
	}
	for _, release := range out.releaseDecisions {
		for _, closureID := range release.ClosureIDs {
			terminal, ok := out.terminalClosures[closureID]
			if !ok {
				out.CorrelationFailures = append(out.CorrelationFailures, harnessAuditRef{
					Type:    release.Binding.EventType,
					Path:    release.Binding.Path,
					Line:    release.Binding.Line,
					Problem: "release-ready event references verification closure not emitted by a terminal atom event",
				})
				continue
			}
			if terminal.ArtifactID != release.Binding.ArtifactID ||
				terminal.PlanRevision != release.Binding.PlanRevision ||
				terminal.PlanDigest != release.Binding.PlanDigest ||
				terminal.WorkspaceFingerprint != release.Binding.WorkspaceFingerprint ||
				terminal.ApprovalEventID != release.Binding.ApprovalEventID {
				out.CorrelationFailures = append(out.CorrelationFailures, harnessAuditRef{
					Type:    release.Binding.EventType,
					Path:    release.Binding.Path,
					Line:    release.Binding.Line,
					Problem: "release-ready event artifact, digest, workspace, or approval binding does not match terminal verification closure",
				})
			}
		}
	}
}

func featurePlannerEventIndicatesCoverageAuthorityBlock(fields map[string]any, event map[string]any) bool {
	for _, value := range []string{
		auditString(fields["validation_diagnostic"]),
		auditString(fields["diagnostic"]),
		auditString(event["message"]),
		auditString(event["error"]),
	} {
		value = strings.ToLower(value)
		if strings.Contains(value, "behavioral_coverage_draft") ||
			strings.Contains(value, "bdd scenarios") ||
			strings.Contains(value, "concrete tdd traceability") {
			return true
		}
	}
	return false
}

func featurePlannerLifecycleFindings(lifecycle *harnessAuditFeaturePlanner) []harnessAuditFinding {
	if lifecycle == nil {
		return nil
	}

	var findings []harnessAuditFinding
	if len(lifecycle.MissingArtifactBindings) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "feature_planner_lifecycle_binding_missing",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d feature-planner lifecycle event(s) were missing artifact or verification closure bindings", len(lifecycle.MissingArtifactBindings)),
			Recommendation: "Emit artifact revision/digest/workspace fields on plan lifecycle events and verification_closure_event_id on terminal atom events.",
			Tools:          []string{"harness_audit"},
			DetailCount:    len(lifecycle.MissingArtifactBindings),
		})
	}
	if lifecycle.CoverageAuthorityBlocks > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "feature_planner_coverage_authority_missing",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d feature-planner plan rejection(s) lacked structured behavioral coverage authority", lifecycle.CoverageAuthorityBlocks),
			Recommendation: "Require accepted product coverage or standalone behavioral_coverage_draft before plan approval, dispatch, or release-readiness claims.",
			Tools:          []string{"harness_audit"},
			DetailCount:    lifecycle.CoverageAuthorityBlocks,
		})
	}
	if len(lifecycle.CorrelationFailures) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "feature_planner_lifecycle_correlation_failed",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d feature-planner release lifecycle correlation failure(s)", len(lifecycle.CorrelationFailures)),
			Recommendation: "Ensure release-ready events reference terminal verification closures with the same artifact revision, digest, workspace fingerprint, and approval event.",
			Tools:          []string{"harness_audit"},
			DetailCount:    len(lifecycle.CorrelationFailures),
		})
	}

	return findings
}

func auditIntentLoopGaps(out *harnessAuditIntentLoop, value any) {
	gaps, ok := value.([]any)
	if !ok {
		return
	}

	for _, gap := range gaps {
		gapMap, ok := gap.(map[string]any)
		if !ok {
			continue
		}

		if class := sanitizedIntentLoopGapRequirement(auditString(gapMap["requirement"])); class != "" {
			out.GapRequirementCounts[class]++
		}
		if status := sanitizedIntentLoopGapStatus(auditString(gapMap["status"])); status != "" {
			out.GapStatusCounts[status]++
		}
	}
}

func sanitizedIntentLoopGapRequirement(requirement string) string {
	requirement = strings.ToLower(strings.TrimSpace(requirement))
	if requirement == "" {
		return ""
	}

	if idx := strings.Index(requirement, ":"); idx > 0 {
		requirement = requirement[:idx]
	}

	switch requirement {
	case "source_sanity", "source-sanity":
		return "source_sanity"
	case "verification", "required_test", "required-test":
		return "verification"
	case "acceptance":
		return "acceptance"
	case "review":
		return "review"
	case "required_path", "required-path", "path_coverage", "path-coverage":
		return "path_coverage"
	default:
		return "other"
	}
}

func sanitizedIntentLoopGapStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "failed", "missing", "untested", "not_met", "not-met", "unparseable", "blocked":
		return strings.ReplaceAll(status, "-", "_")
	default:
		if status == "" {
			return ""
		}
		return "other"
	}
}

func pruneEmptyIntentLoopAuditCounts(out *harnessAuditIntentLoop) {
	if len(out.EventCounts) == 0 {
		out.EventCounts = nil
	}
	if len(out.GapRequirementCounts) == 0 {
		out.GapRequirementCounts = nil
	}
	if len(out.GapStatusCounts) == 0 {
		out.GapStatusCounts = nil
	}
}
