package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/comprehension"
	"idiom/internal/plancontract"
	"idiom/internal/util"
)

type PlanArtifactAuditOptions struct {
	EnforcePacketEvidence bool
}

type PlanArtifactAuditResult struct {
	OK       bool                       `json:"ok"`
	Metrics  PlanArtifactAuditMetrics   `json:"metrics"`
	Findings []PlanArtifactAuditFinding `json:"findings,omitempty"`
}

type PlanArtifactAuditMetrics struct {
	PacketIdentityMismatches      int    `json:"packet_identity_mismatches,omitempty"`
	PacketFreshnessMismatches     int    `json:"packet_freshness_mismatches,omitempty"`
	ScopePathMismatches           int    `json:"scope_path_mismatches,omitempty"`
	EvidenceMismatches            int    `json:"evidence_mismatches,omitempty"`
	UnresolvedQuestionMismatches  int    `json:"unresolved_question_mismatches,omitempty"`
	VerificationObligationSignals int    `json:"verification_obligation_signals,omitempty"`
	ApprovalHandoffMismatches     int    `json:"approval_handoff_mismatches,omitempty"`
	ApprovalScopeMismatches       int    `json:"approval_scope_mismatches,omitempty"`
	PacketAvailable               bool   `json:"packet_available"`
	PacketFresh                   bool   `json:"packet_fresh"`
	SubmittedEditPaths            int    `json:"submitted_edit_paths"`
	PacketScopePaths              int    `json:"packet_scope_paths,omitempty"`
	RequiredVerificationOpenCount int    `json:"required_verification_open_count,omitempty"`
	PacketState                   string `json:"packet_state,omitempty"`
	PacketReason                  string `json:"packet_reason,omitempty"`
	PackID                        string `json:"pack_id,omitempty"`
	PacketIndexID                 string `json:"packet_index_id,omitempty"`
	ApprovalScopeID               string `json:"approval_scope_id,omitempty"`
	HandoffID                     string `json:"handoff_id,omitempty"`
}

type PlanArtifactAuditFinding struct {
	Severity string   `json:"severity"`
	Kind     string   `json:"kind"`
	Message  string   `json:"message"`
	Evidence []string `json:"evidence,omitempty"`
}

func AuditPlanArtifact(root string, artifact PlanArtifact, opts PlanArtifactAuditOptions) PlanArtifactAuditResult {
	audit := planArtifactAuditor{
		root:     strings.TrimSpace(root),
		artifact: NormalizeLoadedPlanArtifact(artifact),
		opts:     opts,
	}
	audit.checkPacketEvidence()
	audit.checkScopeEvidence()
	audit.checkUnresolvedQuestions()
	audit.checkVerificationObligations()
	audit.checkApprovalAndHandoff()
	return audit.result()
}

func (r PlanArtifactAuditResult) ErrorSummary() string {
	var parts []string
	for _, finding := range r.Findings {
		if finding.Severity != "error" {
			continue
		}
		parts = append(parts, finding.Kind)
	}
	if len(parts) == 0 {
		return ""
	}
	slices.Sort(parts)
	return "plan_artifact.audit_errors " + strings.Join(cleanPlanArtifactAuditStrings(parts), "; ")
}

func (r PlanArtifactAuditResult) SummaryLine() string {
	return fmt.Sprintf(
		"audit_ok=%t findings=%d packet_state=%s packet_reason=%s packet_identity_mismatches=%d packet_freshness_mismatches=%d scope_path_mismatches=%d evidence_mismatches=%d unresolved_question_mismatches=%d verification_obligation_signals=%d approval_handoff_mismatches=%d approval_scope_mismatches=%d",
		r.OK,
		len(r.Findings),
		util.FirstNonEmpty(r.Metrics.PacketState, "unknown"),
		util.FirstNonEmpty(r.Metrics.PacketReason, "unknown"),
		r.Metrics.PacketIdentityMismatches,
		r.Metrics.PacketFreshnessMismatches,
		r.Metrics.ScopePathMismatches,
		r.Metrics.EvidenceMismatches,
		r.Metrics.UnresolvedQuestionMismatches,
		r.Metrics.VerificationObligationSignals,
		r.Metrics.ApprovalHandoffMismatches,
		r.Metrics.ApprovalScopeMismatches,
	)
}

type planArtifactAuditor struct {
	root        string
	artifact    PlanArtifact
	opts        PlanArtifactAuditOptions
	packet      comprehension.PackResult
	packetFound bool
	findings    []PlanArtifactAuditFinding
	metrics     PlanArtifactAuditMetrics
}

func (a *planArtifactAuditor) checkPacketEvidence() {
	packID := util.FirstNonEmpty(a.artifact.Packet.PackID, a.artifact.PackID)
	a.markPacketState("missing", "not_provided", false)
	a.metrics.PackID = sanitizePlanArtifactPackID(packID)
	a.metrics.PacketIndexID = sanitizePlanArtifactToken(a.artifact.Packet.IndexID)
	if a.artifact.Packet.PackID != "" && a.artifact.PackID != "" && a.artifact.Packet.PackID != a.artifact.PackID {
		a.add("error", "packet_identity_mismatch", "plan artifact top-level and nested packet ids differ", []string{"pack_id_mirror"})
		a.metrics.PacketIdentityMismatches++
		a.markPacketState("overridden", "packet_id_mirror", true)
	}
	for _, evidence := range a.artifact.Evidence {
		if strings.HasPrefix(evidence.Handle, "context_pack://") {
			evidencePackID := strings.TrimPrefix(evidence.Handle, "context_pack://")
			if packID == "" || evidencePackID != packID {
				a.add(enforcedSeverity(a.opts), "evidence_packet_mismatch", "plan artifact evidence handle does not match submitted packet id", []string{evidence.Handle})
				a.metrics.EvidenceMismatches++
			}
		}
	}
	if packID == "" {
		if packetStatusClaimsAvailable(a.artifact.Packet) {
			a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "plan artifact claims packet availability without a packet id", []string{"packet_status=" + a.artifact.Packet.Status})
			a.metrics.PacketIdentityMismatches++
			a.markPacketState("missing", "availability_claim_without_packet_id", true)
		}
		return
	}
	packet, found, err := loadPlanArtifactAuditPacket(a.root, packID)
	if err != nil {
		a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "submitted packet id could not be loaded", []string{packID, "load_error"})
		a.metrics.PacketIdentityMismatches++
		a.markPacketState("missing", "load_error", a.metrics.PacketState != "overridden")
		return
	}
	if !found {
		a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "submitted packet id is not available in graph view evidence", []string{packID})
		a.metrics.PacketIdentityMismatches++
		a.markPacketState("missing", "cache_miss", a.metrics.PacketState != "overridden")
		return
	}
	a.packet = packet
	a.packetFound = true
	a.metrics.PacketAvailable = true
	a.metrics.PacketFresh = packet.Fresh
	a.metrics.PacketIndexID = sanitizePlanArtifactToken(util.FirstNonEmpty(packet.IndexID, a.artifact.Packet.IndexID))
	if packet.Fresh {
		a.markPacketState("inherited", "fresh_graph_view", a.metrics.PacketState != "overridden")
	} else {
		a.markPacketState("stale", "stale_graph_view", a.metrics.PacketState != "overridden")
	}
	if a.artifact.Packet.IndexID != "" && packet.IndexID != "" && a.artifact.Packet.IndexID != packet.IndexID {
		a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "submitted packet index id differs from loaded graph view", []string{a.artifact.Packet.IndexID, packet.IndexID})
		a.metrics.PacketIdentityMismatches++
	}
	if packetStatusClaimsFresh(a.artifact.Packet) && !packet.Fresh {
		a.add(enforcedSeverity(a.opts), "packet_freshness_mismatch", "plan artifact claims fresh packet evidence but loaded packet is stale", []string{packID})
		a.metrics.PacketFreshnessMismatches++
	}
	if a.artifact.Packet.ArtifactPath != "" && !strings.HasSuffix(filepath.ToSlash(a.artifact.Packet.ArtifactPath), packID+".json") {
		a.add("warning", "packet_identity_mismatch", "packet artifact path does not match submitted packet id", []string{a.artifact.Packet.ArtifactPath})
		a.metrics.PacketIdentityMismatches++
	}
	for _, evidence := range a.artifact.Evidence {
		if evidence.Freshness == "fresh" && !packet.Fresh {
			a.add(enforcedSeverity(a.opts), "packet_freshness_mismatch", "plan evidence claims fresh facts while loaded packet is stale", []string{util.FirstNonEmpty(evidence.ID, evidence.Kind)})
			a.metrics.PacketFreshnessMismatches++
		}
	}
}

func (a *planArtifactAuditor) markPacketState(state string, reason string, overwrite bool) {
	state = sanitizePlanArtifactToken(state)
	reason = sanitizePlanArtifactToken(reason)
	if state == "" {
		return
	}
	if !overwrite && strings.TrimSpace(a.metrics.PacketState) != "" {
		return
	}
	a.metrics.PacketState = state
	a.metrics.PacketReason = reason
}

func (a *planArtifactAuditor) checkScopeEvidence() {
	editPaths := planArtifactEditPaths(a.artifact.Edits)
	a.metrics.SubmittedEditPaths = len(editPaths)
	required := planArtifactAuditStringSet(a.artifact.Paths.Required)
	for _, path := range a.artifact.Contract.RequiredEditPaths {
		required[path] = struct{}{}
	}
	sourceSatisfied := planArtifactAuditStringSet(a.artifact.Paths.SourceSatisfied)
	sourceSatisfiedViolations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        planArtifactAuditSetStrings(required),
		SourceSatisfiedPaths: a.artifact.Paths.SourceSatisfied,
		Evidence:             PlanArtifactSourceSatisfiedEvidenceRefs(a.artifact),
		ApprovedPaths:        a.artifact.Approval.ApprovedPaths,
		PendingPaths:         a.artifact.Approval.PendingPaths,
		RequireEvidence:      true,
	})
	validSourceSatisfied := plancontract.SourceSatisfiedCoveragePathSet(a.artifact.Paths.SourceSatisfied, sourceSatisfiedViolations)
	for _, violation := range sourceSatisfiedViolations {
		a.addSourceSatisfiedViolation(violation)
	}
	editable := planArtifactAuditStringSet(a.artifact.Paths.Editable)
	for _, path := range a.artifact.Paths.Changed {
		editable[path] = struct{}{}
	}
	forbidden := planArtifactAuditStringSet(a.artifact.Paths.Forbidden)
	for _, path := range a.artifact.Contract.ForbiddenPaths {
		forbidden[path] = struct{}{}
	}
	for _, path := range editPaths {
		if _, ok := forbidden[path]; ok {
			a.add("error", "scope_path_mismatch", "plan edit targets a forbidden path", []string{path})
			a.metrics.ScopePathMismatches++
		}
		if len(required) > 0 {
			if _, ok := required[path]; !ok {
				a.add(enforcedSeverity(a.opts), "scope_path_mismatch", "plan edit path is missing from required scope", []string{path})
				a.metrics.ScopePathMismatches++
			}
		}
		if len(editable) > 0 {
			if _, ok := editable[path]; !ok {
				a.add(enforcedSeverity(a.opts), "scope_path_mismatch", "plan edit path is missing from editable or changed scope", []string{path})
				a.metrics.ScopePathMismatches++
			}
		}
	}
	for path := range sourceSatisfied {
		if _, ok := forbidden[path]; ok {
			a.add("error", "scope_path_mismatch", "source-satisfied path targets a forbidden path", []string{path})
			a.metrics.ScopePathMismatches++
		}
	}
	for path := range required {
		if !planArtifactAuditContainsString(editPaths, path) {
			if _, ok := validSourceSatisfied[path]; ok {
				continue
			}
			a.add(enforcedSeverity(a.opts), "scope_path_mismatch", "required scope path has no submitted edit", []string{path})
			a.metrics.ScopePathMismatches++
		}
	}
	if !a.packetFound {
		return
	}
	packetScope := planArtifactPacketScopePaths(a.packet)
	a.metrics.PacketScopePaths = len(packetScope)
	for _, path := range editPaths {
		if _, ok := packetScope[path]; ok {
			continue
		}
		if planArtifactEditCreatesNewPath(a.root, a.artifact.Edits, path) {
			a.add("warning", "scope_path_mismatch", "new-file edit is outside packet scope and should carry explicit create evidence", []string{path})
		} else {
			a.add(enforcedSeverity(a.opts), "scope_path_mismatch", "plan edit path is not present in packet candidate scope", []string{path})
		}
		a.metrics.ScopePathMismatches++
	}
	for _, evidence := range a.artifact.Evidence {
		if evidence.Path == "" {
			continue
		}
		if _, ok := packetScope[evidence.Path]; ok {
			continue
		}
		if planArtifactAuditContainsString(editPaths, evidence.Path) {
			continue
		}
		a.add("warning", "evidence_scope_mismatch", "plan evidence path is outside packet and edit scope", []string{evidence.Path})
		a.metrics.EvidenceMismatches++
	}
}

func (a *planArtifactAuditor) addSourceSatisfiedViolation(violation plancontract.SourceSatisfiedPathViolation) {
	switch violation.Code {
	case "missing_current_state_evidence":
		a.add("error", "source_satisfied_evidence_mismatch", violation.Reason, []string{violation.Path})
		a.metrics.EvidenceMismatches++
	case "pending_approval", "outside_approved_scope":
		a.add("error", "approval_scope_mismatch", violation.Reason, []string{violation.Path})
		a.metrics.ApprovalScopeMismatches++
	default:
		a.add("error", "scope_path_mismatch", violation.Reason, []string{violation.Path})
		a.metrics.ScopePathMismatches++
	}
}

func (a *planArtifactAuditor) checkUnresolvedQuestions() {
	for _, question := range a.artifact.UnresolvedQuestions {
		status := strings.TrimSpace(question.Status)
		if planArtifactQuestionClosed(status) {
			continue
		}
		if question.RequiredBeforeHandoff && a.artifact.Handoff.Ready {
			a.add(enforcedSeverity(a.opts), "unresolved_question_mismatch", "handoff is marked ready while a required unresolved question remains open", []string{util.FirstNonEmpty(question.ID, question.Claim)})
			a.metrics.UnresolvedQuestionMismatches++
		}
	}
}

func (a *planArtifactAuditor) checkVerificationObligations() {
	for _, obligation := range a.artifact.VerificationObligations {
		if obligation.Required && !planArtifactVerificationClosed(obligation.Status) {
			a.metrics.RequiredVerificationOpenCount++
			a.metrics.VerificationObligationSignals++
			a.add("warning", "verification_obligation_open", "required verification obligation remains open for executor or reviewer", []string{util.FirstNonEmpty(obligation.ID, obligation.Kind, obligation.Command, obligation.Path)})
		}
		if obligation.Required && obligation.Kind == "" && obligation.Command == "" && obligation.Path == "" {
			a.add(enforcedSeverity(a.opts), "verification_obligation_mismatch", "required verification obligation has no bounded kind, command, or path", []string{util.FirstNonEmpty(obligation.ID, "verification")})
			a.metrics.VerificationObligationSignals++
		}
	}
}

func (a *planArtifactAuditor) checkApprovalAndHandoff() {
	a.metrics.ApprovalScopeID = sanitizePlanArtifactID(a.artifact.Approval.ScopeID)
	a.metrics.HandoffID = sanitizePlanArtifactID(a.artifact.Handoff.ID)
	scopeSeverity := "warning"
	if a.artifact.Handoff.Ready {
		scopeSeverity = enforcedSeverity(a.opts)
	}
	if a.artifact.Approval.ScopeID == "" {
		a.add(scopeSeverity, "approval_scope_mismatch", "approval scope id is missing", []string{"missing_approval_scope_id"})
		a.metrics.ApprovalScopeMismatches++
	}
	if a.artifact.Handoff.ID == "" {
		a.add(scopeSeverity, "approval_handoff_mismatch", "handoff id is missing", []string{"missing_handoff_id"})
		a.metrics.ApprovalHandoffMismatches++
	}
	if a.artifact.Approval.Required && planArtifactApprovalPartial(a.artifact.Approval.Status) {
		a.checkPartialApprovalScope()
	} else if a.artifact.Approval.Required && !planArtifactApprovalSatisfied(a.artifact.Approval.Status) {
		severity := "warning"
		message := "approval is required but not satisfied"
		if a.artifact.Handoff.Ready {
			severity = "error"
			message = "handoff is marked ready while required approval is not satisfied"
		}
		a.add(severity, "approval_handoff_mismatch", message, []string{util.FirstNonEmpty(a.artifact.Approval.Status, "missing_approval_status")})
		a.metrics.ApprovalHandoffMismatches++
	}
	if a.artifact.Handoff.Ready && a.artifact.Approval.ExecutionPermission == "blocked" {
		a.add(enforcedSeverity(a.opts), "approval_scope_mismatch", "handoff is marked ready while approval execution permission is blocked", []string{util.FirstNonEmpty(a.artifact.Approval.ScopeID, "missing_approval_scope_id")})
		a.metrics.ApprovalScopeMismatches++
	}
	if a.artifact.Handoff.Ready && len(a.artifact.Handoff.Blockers) > 0 {
		a.add(enforcedSeverity(a.opts), "approval_handoff_mismatch", "handoff is marked ready while blockers remain", a.artifact.Handoff.Blockers)
		a.metrics.ApprovalHandoffMismatches++
	}
	if strings.TrimSpace(a.artifact.Handoff.Status) == "ready" && !a.artifact.Handoff.Ready {
		a.add("warning", "approval_handoff_mismatch", "handoff status is ready but ready flag is false", []string{a.artifact.Handoff.ID})
		a.metrics.ApprovalHandoffMismatches++
	}
}

func (a *planArtifactAuditor) checkPartialApprovalScope() {
	severity := "warning"
	if a.artifact.Handoff.Ready {
		severity = "error"
	}
	if len(a.artifact.Approval.ApprovedPaths) == 0 {
		a.add(severity, "approval_scope_mismatch", "partial approval is missing approved scope paths", []string{util.FirstNonEmpty(a.artifact.Approval.ScopeID, "missing_approval_scope_id")})
		a.metrics.ApprovalScopeMismatches++
	}
	if len(a.artifact.Approval.PendingPaths) == 0 {
		a.add(severity, "approval_scope_mismatch", "partial approval is missing pending scope paths", []string{util.FirstNonEmpty(a.artifact.Approval.ScopeID, "missing_approval_scope_id")})
		a.metrics.ApprovalScopeMismatches++
	}
	switch a.artifact.Approval.ExecutionPermission {
	case "execute_approved_scope":
	case "blocked":
		if a.artifact.Handoff.Ready {
			a.add("error", "approval_scope_mismatch", "partial approval cannot execute while execution permission is blocked", []string{util.FirstNonEmpty(a.artifact.Approval.ScopeID, "missing_approval_scope_id")})
			a.metrics.ApprovalScopeMismatches++
		}
	default:
		a.add(severity, "approval_scope_mismatch", "partial approval has ambiguous execution permission", []string{util.FirstNonEmpty(a.artifact.Approval.ExecutionPermission, "missing_execution_permission")})
		a.metrics.ApprovalScopeMismatches++
	}
	if !a.artifact.Handoff.Ready {
		a.add("warning", "approval_handoff_mismatch", "partial approval is not executable until handoff is ready", []string{util.FirstNonEmpty(a.artifact.Approval.ScopeID, "missing_approval_scope_id")})
		a.metrics.ApprovalHandoffMismatches++
		return
	}
	approved := planArtifactAuditStringSet(a.artifact.Approval.ApprovedPaths)
	pending := planArtifactAuditStringSet(a.artifact.Approval.PendingPaths)
	for _, path := range planArtifactEditPaths(a.artifact.Edits) {
		if _, ok := pending[path]; ok {
			a.add("error", "approval_scope_mismatch", "plan edit path is still pending approval", []string{path})
			a.metrics.ApprovalScopeMismatches++
			continue
		}
		if len(approved) > 0 {
			if _, ok := approved[path]; !ok {
				a.add("error", "approval_scope_mismatch", "plan edit path is outside approved scope", []string{path})
				a.metrics.ApprovalScopeMismatches++
			}
		}
	}
}

func (a *planArtifactAuditor) result() PlanArtifactAuditResult {
	ok := true
	for _, finding := range a.findings {
		if finding.Severity == "error" {
			ok = false
			break
		}
	}
	return PlanArtifactAuditResult{OK: ok, Metrics: a.metrics, Findings: a.findings}
}

func (a *planArtifactAuditor) add(severity string, kind string, message string, evidence []string) {
	item := PlanArtifactAuditFinding{
		Severity: sanitizePlanArtifactToken(util.FirstNonEmpty(severity, "warning")),
		Kind:     sanitizePlanArtifactToken(kind),
		Message:  sanitizePlanArtifactText(message, 240),
		Evidence: normalizePlanArtifactAuditEvidence(evidence),
	}
	a.findings = append(a.findings, item)
}

func loadPlanArtifactAuditPacket(root string, packID string) (comprehension.PackResult, bool, error) {
	view, status, err := comprehension.LoadViewByIDStatus(root, packID)
	if err != nil {
		return comprehension.PackResult{}, false, err
	}
	if status.State == comprehension.ViewLoadStateFresh || status.State == comprehension.ViewLoadStateStale {
		return view, true, nil
	}
	path := filepath.Join(strings.TrimSpace(root), ".idiom", "comprehension", "views", packID+".json")
	body, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return comprehension.PackResult{}, false, nil
	}
	if err != nil {
		return comprehension.PackResult{}, false, err
	}
	if err := json.Unmarshal(body, &view); err != nil {
		return comprehension.PackResult{}, false, err
	}
	if strings.TrimSpace(view.PackID) == "" {
		view.PackID = packID
	}
	return view, true, nil
}

func planArtifactPacketScopePaths(packet comprehension.PackResult) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range packet.CandidateFiles {
		if path := sanitizePlanArtifactPathValue(file.Path); path != "" {
			out[path] = struct{}{}
		}
	}
	for _, symbol := range packet.CandidateSymbols {
		if path := sanitizePlanArtifactPathValue(symbol.Path); path != "" {
			out[path] = struct{}{}
		}
	}
	for _, test := range packet.Tests {
		if path := sanitizePlanArtifactPathValue(test.Path); path != "" {
			out[path] = struct{}{}
		}
	}
	return out
}

func planArtifactEditCreatesNewPath(root string, edits []PlanEdit, path string) bool {
	for _, edit := range edits {
		if edit.Path != path || !planEditIsWholeFileWrite(edit) {
			continue
		}
		full := edit.Path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, edit.Path)
		}
		if _, err := os.Stat(filepath.Clean(full)); os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func packetStatusClaimsAvailable(packet PlanArtifactPacket) bool {
	status := strings.TrimSpace(packet.Status)
	return status == "available" || status == "provided" || status == "fresh"
}

func packetStatusClaimsFresh(packet PlanArtifactPacket) bool {
	return strings.TrimSpace(packet.Freshness) == "fresh" || strings.TrimSpace(packet.Status) == "fresh"
}

func planArtifactQuestionClosed(status string) bool {
	switch strings.TrimSpace(status) {
	case "closed", "resolved", "answered", "not_applicable", "n_a":
		return true
	default:
		return false
	}
}

func planArtifactVerificationClosed(status string) bool {
	switch strings.TrimSpace(status) {
	case "passed", "complete", "closed", "satisfied", "not_applicable", "n_a":
		return true
	default:
		return false
	}
}

func planArtifactApprovalSatisfied(status string) bool {
	switch strings.TrimSpace(status) {
	case "approved", "implicit", "not_required", "n_a":
		return true
	default:
		return false
	}
}

func normalizePlanArtifactAuditEvidence(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if path := sanitizePlanArtifactPathValue(value); path != "" {
			out = append(out, path)
			continue
		}
		if token := sanitizePlanArtifactToken(value); token != "" {
			out = append(out, token)
			continue
		}
		if text := sanitizePlanArtifactText(value, 120); text != "" {
			out = append(out, text)
		}
		if len(out) >= 8 {
			break
		}
	}
	return cleanPlanArtifactAuditStrings(out)
}

func enforcedSeverity(opts PlanArtifactAuditOptions) string {
	if opts.EnforcePacketEvidence {
		return "error"
	}
	return "warning"
}

func planArtifactAuditStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func planArtifactAuditSetStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func planArtifactAuditContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cleanPlanArtifactAuditStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
