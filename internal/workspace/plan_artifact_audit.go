package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/comprehension"
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
	PacketIdentityMismatches      int  `json:"packet_identity_mismatches,omitempty"`
	PacketFreshnessMismatches     int  `json:"packet_freshness_mismatches,omitempty"`
	ScopePathMismatches           int  `json:"scope_path_mismatches,omitempty"`
	EvidenceMismatches            int  `json:"evidence_mismatches,omitempty"`
	UnresolvedQuestionMismatches  int  `json:"unresolved_question_mismatches,omitempty"`
	VerificationObligationSignals int  `json:"verification_obligation_signals,omitempty"`
	ApprovalHandoffMismatches     int  `json:"approval_handoff_mismatches,omitempty"`
	PacketAvailable               bool `json:"packet_available"`
	PacketFresh                   bool `json:"packet_fresh"`
	SubmittedEditPaths            int  `json:"submitted_edit_paths"`
	PacketScopePaths              int  `json:"packet_scope_paths,omitempty"`
	RequiredVerificationOpenCount int  `json:"required_verification_open_count,omitempty"`
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
	sort.Strings(parts)
	return "plan_artifact.audit_errors " + strings.Join(cleanPlanArtifactAuditStrings(parts), "; ")
}

func (r PlanArtifactAuditResult) SummaryLine() string {
	return fmt.Sprintf(
		"audit_ok=%t findings=%d packet_identity_mismatches=%d packet_freshness_mismatches=%d scope_path_mismatches=%d evidence_mismatches=%d unresolved_question_mismatches=%d verification_obligation_signals=%d approval_handoff_mismatches=%d",
		r.OK,
		len(r.Findings),
		r.Metrics.PacketIdentityMismatches,
		r.Metrics.PacketFreshnessMismatches,
		r.Metrics.ScopePathMismatches,
		r.Metrics.EvidenceMismatches,
		r.Metrics.UnresolvedQuestionMismatches,
		r.Metrics.VerificationObligationSignals,
		r.Metrics.ApprovalHandoffMismatches,
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
	packID := firstNonEmptyString(a.artifact.Packet.PackID, a.artifact.PackID)
	if a.artifact.Packet.PackID != "" && a.artifact.PackID != "" && a.artifact.Packet.PackID != a.artifact.PackID {
		a.add("error", "packet_identity_mismatch", "plan artifact top-level and nested packet ids differ", []string{"pack_id_mirror"})
		a.metrics.PacketIdentityMismatches++
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
		}
		return
	}
	packet, found, err := loadPlanArtifactAuditPacket(a.root, packID)
	if err != nil {
		a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "submitted packet id could not be loaded", []string{packID, "load_error"})
		a.metrics.PacketIdentityMismatches++
		return
	}
	if !found {
		a.add(enforcedSeverity(a.opts), "packet_identity_mismatch", "submitted packet id is not available in graph view evidence", []string{packID})
		a.metrics.PacketIdentityMismatches++
		return
	}
	a.packet = packet
	a.packetFound = true
	a.metrics.PacketAvailable = true
	a.metrics.PacketFresh = packet.Fresh
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
			a.add(enforcedSeverity(a.opts), "packet_freshness_mismatch", "plan evidence claims fresh facts while loaded packet is stale", []string{firstNonEmptyString(evidence.ID, evidence.Kind)})
			a.metrics.PacketFreshnessMismatches++
		}
	}
}

func (a *planArtifactAuditor) checkScopeEvidence() {
	editPaths := planArtifactEditPaths(a.artifact.Edits)
	a.metrics.SubmittedEditPaths = len(editPaths)
	required := planArtifactAuditStringSet(a.artifact.Paths.Required)
	for _, path := range a.artifact.Contract.RequiredEditPaths {
		required[path] = struct{}{}
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
	for path := range required {
		if !planArtifactAuditContainsString(editPaths, path) {
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

func (a *planArtifactAuditor) checkUnresolvedQuestions() {
	for _, question := range a.artifact.UnresolvedQuestions {
		status := strings.TrimSpace(question.Status)
		if planArtifactQuestionClosed(status) {
			continue
		}
		if question.RequiredBeforeHandoff && a.artifact.Handoff.Ready {
			a.add(enforcedSeverity(a.opts), "unresolved_question_mismatch", "handoff is marked ready while a required unresolved question remains open", []string{firstNonEmptyString(question.ID, question.Claim)})
			a.metrics.UnresolvedQuestionMismatches++
		}
	}
}

func (a *planArtifactAuditor) checkVerificationObligations() {
	for _, obligation := range a.artifact.VerificationObligations {
		if obligation.Required && !planArtifactVerificationClosed(obligation.Status) {
			a.metrics.RequiredVerificationOpenCount++
			a.metrics.VerificationObligationSignals++
			a.add("warning", "verification_obligation_open", "required verification obligation remains open for executor or reviewer", []string{firstNonEmptyString(obligation.ID, obligation.Kind, obligation.Command, obligation.Path)})
		}
		if obligation.Required && obligation.Kind == "" && obligation.Command == "" && obligation.Path == "" {
			a.add(enforcedSeverity(a.opts), "verification_obligation_mismatch", "required verification obligation has no bounded kind, command, or path", []string{firstNonEmptyString(obligation.ID, "verification")})
			a.metrics.VerificationObligationSignals++
		}
	}
}

func (a *planArtifactAuditor) checkApprovalAndHandoff() {
	if a.artifact.Approval.Required && !planArtifactApprovalSatisfied(a.artifact.Approval.Status) {
		severity := "warning"
		message := "approval is required but not satisfied"
		if a.artifact.Handoff.Ready {
			severity = "error"
			message = "handoff is marked ready while required approval is not satisfied"
		}
		a.add(severity, "approval_handoff_mismatch", message, []string{firstNonEmptyString(a.artifact.Approval.Status, "missing_approval_status")})
		a.metrics.ApprovalHandoffMismatches++
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
		Severity: sanitizePlanArtifactToken(firstNonEmptyString(severity, "warning")),
		Kind:     sanitizePlanArtifactToken(kind),
		Message:  sanitizePlanArtifactText(message, 240),
		Evidence: normalizePlanArtifactAuditEvidence(evidence),
	}
	a.findings = append(a.findings, item)
}

func loadPlanArtifactAuditPacket(root string, packID string) (comprehension.PackResult, bool, error) {
	view, hit, err := comprehension.LoadViewByID(root, packID)
	if err != nil {
		return comprehension.PackResult{}, false, err
	}
	if hit {
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
