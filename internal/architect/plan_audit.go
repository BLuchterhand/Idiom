package architect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type PlanAuditResult struct {
	OK              bool                     `json:"ok"`
	MismatchMetrics PlanAuditMismatchMetrics `json:"mismatch_metrics"`
	Findings        []PlanAuditFinding       `json:"findings,omitempty"`
}

type PlanAuditFinding struct {
	Severity            string   `json:"severity"`
	Kind                string   `json:"kind"`
	Message             string   `json:"message"`
	Evidence            []string `json:"evidence,omitempty"`
	SuggestedResolution string   `json:"suggested_resolution,omitempty"`
}

type PlanAuditMismatchMetrics struct {
	PacketIdentityMismatches      int `json:"packet_identity_mismatches,omitempty"`
	PacketFreshnessMismatches     int `json:"packet_freshness_mismatches,omitempty"`
	ScopePathMismatches           int `json:"scope_path_mismatches,omitempty"`
	EvidenceMismatches            int `json:"evidence_mismatches,omitempty"`
	UnresolvedQuestionMismatches  int `json:"unresolved_question_mismatches,omitempty"`
	VerificationObligationSignals int `json:"verification_obligation_signals,omitempty"`
	ApprovalHandoffMismatches     int `json:"approval_handoff_mismatches,omitempty"`
}

type PlanArtifactAuditSubject struct {
	SchemaVersion           string                             `json:"schema_version,omitempty"`
	PackID                  string                             `json:"pack_id,omitempty"`
	Packet                  PlanArtifactPacketAuditSubject     `json:"packet,omitempty"`
	Paths                   PlanArtifactPathsAuditSubject      `json:"paths,omitempty"`
	Evidence                []PlanArtifactEvidenceAuditSubject `json:"evidence,omitempty"`
	UnresolvedQuestions     []PlanArtifactQuestionAuditSubject `json:"unresolved_questions,omitempty"`
	VerificationObligations []PlanArtifactVerifyAuditSubject   `json:"verification_obligations,omitempty"`
	Approval                PlanArtifactApprovalAuditSubject   `json:"approval,omitempty"`
	Handoff                 PlanArtifactHandoffAuditSubject    `json:"handoff,omitempty"`
	Edits                   []PlanArtifactEditAuditSubject     `json:"edits,omitempty"`
}

type PlanArtifactPacketAuditSubject struct {
	PackID       string `json:"pack_id,omitempty"`
	Status       string `json:"status,omitempty"`
	IndexID      string `json:"index_id,omitempty"`
	Freshness    string `json:"freshness,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
}

type PlanArtifactPathsAuditSubject struct {
	Editable         []string `json:"editable,omitempty"`
	Required         []string `json:"required,omitempty"`
	ReadOnlyEvidence []string `json:"read_only_evidence,omitempty"`
	Forbidden        []string `json:"forbidden,omitempty"`
	Changed          []string `json:"changed,omitempty"`
}

type PlanArtifactEvidenceAuditSubject struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Handle    string `json:"handle,omitempty"`
	Path      string `json:"path,omitempty"`
	Freshness string `json:"freshness,omitempty"`
}

type PlanArtifactQuestionAuditSubject struct {
	ID                    string `json:"id,omitempty"`
	Claim                 string `json:"claim,omitempty"`
	Status                string `json:"status,omitempty"`
	RequiredBeforeHandoff bool   `json:"required_before_handoff,omitempty"`
}

type PlanArtifactVerifyAuditSubject struct {
	ID       string `json:"id,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Status   string `json:"status,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type PlanArtifactApprovalAuditSubject struct {
	Required bool   `json:"required"`
	Status   string `json:"status,omitempty"`
	ScopeID  string `json:"scope_id,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

type PlanArtifactHandoffAuditSubject struct {
	ID       string   `json:"id,omitempty"`
	Status   string   `json:"status,omitempty"`
	Ready    bool     `json:"ready"`
	Blockers []string `json:"blockers,omitempty"`
}

type PlanArtifactEditAuditSubject struct {
	Path string `json:"path"`
}

var (
	planPathPattern        = regexp.MustCompile(`(?:^|[\s'"\(\[` + "`" + `])([A-Za-z0-9_./-]+\.(?:go|rs|pyi|py|tsx|tscn|ts|jsx|json|js|java|cxx|cpp|cc|c|hxx|hpp|hh|h|gd|tres|md|ya?ml|toml|sh|sql)(?::\d+)?)(?:$|[\s'"\)\]\},;.` + "`" + `])`)
	planHandlePattern      = regexp.MustCompile(`\b(?:file|symbol|test|trace|context_pack)://[^\s\]\),;` + "`" + `]+`)
	planCommandPattern     = regexp.MustCompile("`([^`]*(?:go test|make test|make build|go build|npm test|pnpm test|yarn test|bash |sh |python |python3 |pytest|godot|scons|\\./scripts/|scripts/)[^`]*)`")
	planPlaceholderPattern = regexp.MustCompile(`<(?:path|count|file|command|test|todo|unknown)>`)
)

func AuditPlan(packet Packet, plan string) PlanAuditResult {
	plan = strings.TrimSpace(plan)
	findings := make([]PlanAuditFinding, 0)
	if plan == "" {
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "empty_plan",
			Message:             "candidate plan is empty",
			SuggestedResolution: "Provide the model-authored plan text before running plan audit.",
		})
		return finalizePlanAuditResult(findings)
	}
	findings = append(findings, auditPlanPacketIdentity(packet, plan)...)
	findings = append(findings, auditPlanPaths(packet, plan)...)
	findings = append(findings, auditPlanScopeEvidence(packet, plan)...)
	findings = append(findings, auditPlanHandles(packet, plan)...)
	findings = append(findings, auditPlanValidation(packet, plan)...)
	findings = append(findings, auditPlanCommandReferences(packet, plan)...)
	findings = append(findings, auditPlanFreshness(packet, plan)...)
	findings = append(findings, auditPlanUnresolvedQuestions(packet, plan)...)
	findings = append(findings, auditProtectedPaths(packet, plan)...)
	findings = append(findings, auditPlanReadiness(packet, plan)...)
	findings = append(findings, auditPlanPlaceholders(plan)...)
	return finalizePlanAuditResult(findings)
}

func AuditPlanArtifact(packet Packet, artifact PlanArtifactAuditSubject) PlanAuditResult {
	var findings []PlanAuditFinding
	findings = append(findings, auditArtifactPacketIdentity(packet, artifact)...)
	findings = append(findings, auditArtifactScopeEvidence(packet, artifact)...)
	findings = append(findings, auditArtifactUnresolvedQuestions(artifact)...)
	findings = append(findings, auditArtifactVerificationObligations(packet, artifact)...)
	findings = append(findings, auditArtifactApprovalHandoff(artifact)...)
	return finalizePlanAuditResult(findings)
}

func auditPlanCommandReferences(packet Packet, plan string) []PlanAuditFinding {
	root := strings.TrimSpace(packet.Workspace.Root)
	if root == "" {
		return nil
	}
	var findings []PlanAuditFinding
	for _, command := range extractPlanCommands(plan) {
		for _, rel := range commandReferencePaths(command) {
			if pathExistsUnderRoot(root, rel) {
				continue
			}
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "unknown_command_path",
				Message:             fmt.Sprintf("plan validation command %q references missing path %q", command, rel),
				Evidence:            []string{command, rel},
				SuggestedResolution: "Correct the command path, add the path as an explicit new artifact, or mark validation blocked with the exact missing command.",
			})
		}
	}
	return findings
}

func auditPlanPacketIdentity(packet Packet, plan string) []PlanAuditFinding {
	knownPackIDs := packetPackIDs(packet)
	if len(knownPackIDs) == 0 {
		return nil
	}
	raw := planHandlePattern.FindAllString(plan, -1)
	var findings []PlanAuditFinding
	for _, handle := range cleanStrings(raw) {
		if !strings.HasPrefix(handle, "context_pack://") {
			continue
		}
		packID := strings.TrimPrefix(handle, "context_pack://")
		if _, ok := knownPackIDs[packID]; ok {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "packet_identity_mismatch",
			Message:             "plan cites a context packet id that is not present in packet evidence",
			Evidence:            []string{handle},
			SuggestedResolution: "Use the packet-backed context id, remove the unsupported packet claim, or mark the packet identity as unresolved.",
		})
	}
	return findings
}

func auditPlanScopeEvidence(packet Packet, plan string) []PlanAuditFinding {
	scopePaths := packetScopePaths(packet)
	if len(scopePaths) == 0 {
		return nil
	}
	var findings []PlanAuditFinding
	lines := strings.Split(plan, "\n")
	for _, path := range extractPlanPaths(plan) {
		cleanPath := trimPlanPathLineSuffix(path)
		if cleanPath == "" || pathAllowedAsNew(cleanPath, packetFileRoles(packet), lines) {
			continue
		}
		if _, ok := scopePaths[cleanPath]; ok {
			continue
		}
		if lineFor(plan, cleanPath, mentionsEditVerb) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "scope_path_mismatch",
				Message:             "plan proposes editing a path outside packet candidate scope",
				Evidence:            []string{cleanPath},
				SuggestedResolution: "Add packet-backed scope evidence for the path, mark it as a new file, or keep the plan inside the packet scope.",
			})
		}
	}
	return findings
}

func auditPlanUnresolvedQuestions(packet Packet, plan string) []PlanAuditFinding {
	if len(packet.Unknowns) == 0 && (packet.Handoff == nil || len(packet.Handoff.Blockers) == 0) {
		return nil
	}
	lower := strings.ToLower(plan)
	if mentionsUnresolved(lower) || strings.Contains(lower, "unknown") {
		return nil
	}
	var evidence []string
	evidence = append(evidence, packet.Unknowns...)
	if packet.Handoff != nil {
		evidence = append(evidence, packet.Handoff.Blockers...)
	}
	return []PlanAuditFinding{{
		Severity:            "warning",
		Kind:                "unresolved_question_mismatch",
		Message:             "packet carries unresolved questions or blockers, but the plan does not acknowledge them",
		Evidence:            cleanStrings(evidence),
		SuggestedResolution: "Carry unresolved packet facts into the plan as closed evidence or an explicit blocker.",
	}}
}

func auditPlanPlaceholders(plan string) []PlanAuditFinding {
	raw := planPlaceholderPattern.FindAllString(plan, -1)
	if len(raw) == 0 {
		return nil
	}
	placeholders := cleanStrings(raw)
	return []PlanAuditFinding{{
		Severity:            "error",
		Kind:                "unresolved_placeholder",
		Message:             "plan still contains unresolved placeholder token(s)",
		Evidence:            placeholders,
		SuggestedResolution: "Replace placeholders with packet-backed values or mark the exact unresolved fact as a blocker.",
	}}
}

func auditPlanPaths(packet Packet, plan string) []PlanAuditFinding {
	paths := extractPlanPaths(plan)
	if len(paths) == 0 {
		return nil
	}
	roles := packetFileRoles(packet)
	var findings []PlanAuditFinding
	lines := strings.Split(plan, "\n")
	for _, path := range paths {
		cleanPath := strings.TrimSuffix(path, ":")
		if idx := strings.LastIndex(cleanPath, ":"); idx > 1 {
			if allDigits(cleanPath[idx+1:]) {
				cleanPath = cleanPath[:idx]
			}
		}
		if pathAllowedAsNew(cleanPath, roles, lines) {
			continue
		}
		if fileExistsUnderRoot(packet.Workspace.Root, cleanPath) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "missing_file",
			Message:             fmt.Sprintf("plan cites %q, but that file does not exist and is not marked as new", cleanPath),
			Evidence:            []string{cleanPath},
			SuggestedResolution: "Mark the file as new/create in the plan, correct the path, or remove the unsupported reference.",
		})
	}
	return findings
}

func auditPlanHandles(packet Packet, plan string) []PlanAuditFinding {
	known := packetHandles(packet)
	raw := planHandlePattern.FindAllString(plan, -1)
	if len(raw) == 0 {
		return nil
	}
	var findings []PlanAuditFinding
	for _, handle := range cleanStrings(raw) {
		if _, ok := known[handle]; ok {
			continue
		}
		if strings.HasPrefix(handle, "context_pack://") {
			continue
		}
		if lineFor(plan, handle, mentionsUnresolved) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "unresolved_handle",
			Message:             fmt.Sprintf("plan cites handle %q that is not present in the packet evidence or candidate scope", handle),
			Evidence:            []string{handle},
			SuggestedResolution: "Use a packet handle, mark the handle unresolved, or remove the unsupported citation.",
		})
	}
	return findings
}

func auditPlanValidation(packet Packet, plan string) []PlanAuditFinding {
	var findings []PlanAuditFinding
	commands := packetValidationCommands(packet)
	if len(commands) > 0 {
		mentioned := false
		for _, command := range commands {
			if strings.Contains(plan, command) {
				mentioned = true
				break
			}
		}
		if !mentioned {
			findings = append(findings, PlanAuditFinding{
				Severity:            "warning",
				Kind:                "validation_gap",
				Message:             "packet lists validation commands, but the plan does not mention any of them",
				Evidence:            commands,
				SuggestedResolution: "Carry at least one packet validation command into the plan or explain why validation is blocked.",
			})
		}
	}
	for _, command := range extractPlanCommands(plan) {
		if !looksScopedCommand(command) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "warning",
				Kind:                "unscoped_validation",
				Message:             fmt.Sprintf("plan lists broad or ambiguous validation command %q", command),
				Evidence:            []string{command},
				SuggestedResolution: "Prefer the narrowest package or target command that exercises the changed behavior.",
			})
		}
	}
	return findings
}

func auditPlanFreshness(packet Packet, plan string) []PlanAuditFinding {
	if packet.Freshness == nil || packet.Freshness.Fresh == nil || *packet.Freshness.Fresh {
		return nil
	}
	lower := strings.ToLower(plan)
	if strings.Contains(lower, "stale") || strings.Contains(lower, "refresh") {
		return nil
	}
	return []PlanAuditFinding{{
		Severity:            "warning",
		Kind:                "stale_fact",
		Message:             "packet contains stale context evidence, but the plan does not label or refresh it",
		Evidence:            append([]string(nil), packet.Freshness.StaleNodes...),
		SuggestedResolution: "Label stale evidence in the plan or add a refresh step before relying on it.",
	}}
}

func auditProtectedPaths(packet Packet, plan string) []PlanAuditFinding {
	if packet.Contracts == nil || len(packet.Contracts.ProtectedPaths) == 0 {
		return nil
	}
	var findings []PlanAuditFinding
	for _, protected := range packet.Contracts.ProtectedPaths {
		if protected == "" {
			continue
		}
		if lineFor(plan, protected, mentionsEditVerb) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "protected_path",
				Message:             fmt.Sprintf("plan proposes changing protected path %q", protected),
				Evidence:            []string{protected},
				SuggestedResolution: "Remove the protected-path edit or get an explicit packet permission before implementation.",
			})
		}
	}
	return findings
}

func auditPlanReadiness(packet Packet, plan string) []PlanAuditFinding {
	lower := strings.ToLower(plan)
	var findings []PlanAuditFinding
	if taskSuggestsImplementation(packet.Task) && len(extractPlanPaths(plan)) == 0 && len(packet.Files) > 0 {
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "edit_readiness",
			Message:             "task appears to ask for edits, but the plan does not name a file to change",
			SuggestedResolution: "Add a bounded implementation atom with the file path, or state the concrete blocker.",
		})
	}
	if (strings.Contains(lower, "inspect more") || strings.Contains(lower, "look into more") || strings.Contains(lower, "need more context")) && !mentionsUnresolved(lower) {
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "missing_exact_gap",
			Message:             "plan defers to more inspection without naming an exact missing claim",
			SuggestedResolution: "Name the missing claim and the evidence category that would resolve it.",
		})
	}
	return findings
}

func auditArtifactPacketIdentity(packet Packet, artifact PlanArtifactAuditSubject) []PlanAuditFinding {
	knownPackIDs := packetPackIDs(packet)
	packID := firstNonEmpty(artifact.Packet.PackID, artifact.PackID)
	var findings []PlanAuditFinding
	if len(knownPackIDs) > 0 {
		if packID == "" {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "packet_identity_mismatch",
				Message:             "submitted plan artifact is missing packet id while audit packet carries context views",
				SuggestedResolution: "Carry the packet id from the planning packet into submit_plan.packet.pack_id.",
			})
		} else if _, ok := knownPackIDs[packID]; !ok {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "packet_identity_mismatch",
				Message:             "submitted plan artifact packet id is not present in audit packet evidence",
				Evidence:            []string{packID},
				SuggestedResolution: "Use the packet-backed context id or re-run the plan against the matching packet.",
			})
		}
	}
	if artifact.Packet.PackID != "" && artifact.PackID != "" && artifact.Packet.PackID != artifact.PackID {
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "packet_identity_mismatch",
			Message:             "submitted plan artifact has inconsistent top-level and nested packet ids",
			Evidence:            []string{"pack_id_mirror"},
			SuggestedResolution: "Keep one packet identity across the artifact.",
		})
	}
	if packet.Freshness != nil {
		if artifact.Packet.IndexID != "" && packet.Freshness.IndexID != "" && artifact.Packet.IndexID != packet.Freshness.IndexID {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "packet_identity_mismatch",
				Message:             "submitted plan artifact index id differs from audit packet freshness evidence",
				Evidence:            []string{artifact.Packet.IndexID, packet.Freshness.IndexID},
				SuggestedResolution: "Re-submit the plan from the current packet or mark stale evidence explicitly.",
			})
		}
		if artifactClaimsFresh(artifact.Packet) && packet.Freshness.Fresh != nil && !*packet.Freshness.Fresh {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "packet_freshness_mismatch",
				Message:             "submitted plan artifact claims fresh packet evidence while audit packet is stale",
				Evidence:            append([]string(nil), packet.Freshness.StaleNodes...),
				SuggestedResolution: "Refresh the packet or downgrade the plan artifact freshness claim.",
			})
		}
	}
	for _, evidence := range artifact.Evidence {
		if !strings.HasPrefix(evidence.Handle, "context_pack://") {
			continue
		}
		evidencePackID := strings.TrimPrefix(evidence.Handle, "context_pack://")
		if packID == "" || evidencePackID != packID {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "evidence_packet_mismatch",
				Message:             "submitted plan evidence handle does not match artifact packet id",
				Evidence:            []string{evidence.Handle},
				SuggestedResolution: "Keep evidence handles aligned with the submitted packet id.",
			})
		}
	}
	return findings
}

func auditArtifactScopeEvidence(packet Packet, artifact PlanArtifactAuditSubject) []PlanAuditFinding {
	scopePaths := packetScopePaths(packet)
	editPaths := cleanStrings(planArtifactAuditEditPaths(artifact.Edits))
	required := stringSetFromStrings(artifact.Paths.Required)
	editable := stringSetFromStrings(artifact.Paths.Editable)
	for _, path := range artifact.Paths.Changed {
		editable[path] = struct{}{}
	}
	forbidden := stringSetFromStrings(artifact.Paths.Forbidden)
	var findings []PlanAuditFinding
	for _, path := range editPaths {
		if _, ok := forbidden[path]; ok {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "scope_path_mismatch",
				Message:             "submitted plan edit targets a forbidden path",
				Evidence:            []string{path},
				SuggestedResolution: "Remove the forbidden-path edit or update the packet contract before planning.",
			})
		}
		if len(required) > 0 {
			if _, ok := required[path]; !ok {
				findings = append(findings, PlanAuditFinding{
					Severity:            "error",
					Kind:                "scope_path_mismatch",
					Message:             "submitted plan edit path is missing from required scope",
					Evidence:            []string{path},
					SuggestedResolution: "Align submitted edits with the artifact's required path scope.",
				})
			}
		}
		if len(editable) > 0 {
			if _, ok := editable[path]; !ok {
				findings = append(findings, PlanAuditFinding{
					Severity:            "error",
					Kind:                "scope_path_mismatch",
					Message:             "submitted plan edit path is missing from editable or changed scope",
					Evidence:            []string{path},
					SuggestedResolution: "Carry the edit path into editable/changed scope or remove the unsupported edit.",
				})
			}
		}
		if len(scopePaths) > 0 {
			if _, ok := scopePaths[path]; !ok {
				findings = append(findings, PlanAuditFinding{
					Severity:            "error",
					Kind:                "scope_path_mismatch",
					Message:             "submitted plan edit path is outside audit packet candidate scope",
					Evidence:            []string{path},
					SuggestedResolution: "Use packet-backed scope evidence or rebuild the packet for the submitted plan.",
				})
			}
		}
	}
	for path := range required {
		if !stringSliceContains(editPaths, path) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "scope_path_mismatch",
				Message:             "required artifact scope path has no submitted edit",
				Evidence:            []string{path},
				SuggestedResolution: "Submit an edit for the required path or remove it from required scope.",
			})
		}
	}
	for _, evidence := range artifact.Evidence {
		if evidence.Path == "" {
			continue
		}
		if _, ok := scopePaths[evidence.Path]; ok {
			continue
		}
		if stringSliceContains(editPaths, evidence.Path) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "evidence_scope_mismatch",
			Message:             "submitted plan evidence path is outside packet and edit scope",
			Evidence:            []string{evidence.Path},
			SuggestedResolution: "Keep plan evidence references packet-backed, edit-scoped, or explicitly unresolved.",
		})
	}
	return findings
}

func auditArtifactUnresolvedQuestions(artifact PlanArtifactAuditSubject) []PlanAuditFinding {
	var findings []PlanAuditFinding
	for _, question := range artifact.UnresolvedQuestions {
		if !question.RequiredBeforeHandoff || artifactQuestionClosed(question.Status) || !artifact.Handoff.Ready {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "unresolved_question_mismatch",
			Message:             "submitted plan handoff is ready while a required unresolved question remains open",
			Evidence:            []string{firstNonEmpty(question.ID, question.Claim)},
			SuggestedResolution: "Close the question with evidence or mark the handoff blocked.",
		})
	}
	return findings
}

func auditArtifactVerificationObligations(packet Packet, artifact PlanArtifactAuditSubject) []PlanAuditFinding {
	packetCommands := packetValidationCommands(packet)
	var findings []PlanAuditFinding
	for _, command := range packetCommands {
		if artifactVerificationMentionsCommand(artifact.VerificationObligations, command) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "validation_gap",
			Message:             "submitted plan artifact does not carry a packet validation obligation",
			Evidence:            []string{command},
			SuggestedResolution: "Carry packet validation into verification_obligations or mark validation blocked.",
		})
	}
	for _, obligation := range artifact.VerificationObligations {
		if obligation.Required && !artifactVerificationClosed(obligation.Status) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "warning",
				Kind:                "verification_obligation_open",
				Message:             "submitted plan carries an open required verification obligation",
				Evidence:            []string{firstNonEmpty(obligation.ID, obligation.Kind, obligation.Command, obligation.Path)},
				SuggestedResolution: "Keep this obligation visible through executor and review evidence.",
			})
		}
	}
	return findings
}

func auditArtifactApprovalHandoff(artifact PlanArtifactAuditSubject) []PlanAuditFinding {
	var findings []PlanAuditFinding
	if artifact.Approval.Required && !artifactApprovalSatisfied(artifact.Approval.Status) {
		severity := "warning"
		message := "submitted plan approval is required but not satisfied"
		if artifact.Handoff.Ready {
			severity = "error"
			message = "submitted plan handoff is ready while required approval is not satisfied"
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            severity,
			Kind:                "approval_handoff_mismatch",
			Message:             message,
			Evidence:            []string{firstNonEmpty(artifact.Approval.Status, "missing_approval_status")},
			SuggestedResolution: "Approve the bounded scope or mark the handoff blocked.",
		})
	}
	if artifact.Handoff.Ready && len(artifact.Handoff.Blockers) > 0 {
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "approval_handoff_mismatch",
			Message:             "submitted plan handoff is ready while blockers remain",
			Evidence:            artifact.Handoff.Blockers,
			SuggestedResolution: "Clear blockers with evidence or mark handoff not ready.",
		})
	}
	return findings
}

func extractPlanPaths(plan string) []string {
	matches := planPathPattern.FindAllStringSubmatch(plan, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.Trim(match[1], " \t\n\r'\"`()[]{}.,;")
		if path != "" {
			out = append(out, path)
		}
	}
	return cleanStrings(out)
}

func extractPlanCommands(plan string) []string {
	matches := planCommandPattern.FindAllStringSubmatch(plan, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 2 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return cleanStrings(out)
}

func packetFileRoles(packet Packet) map[string]string {
	out := map[string]string{}
	for _, file := range packet.Files {
		out[file.Path] = strings.ToLower(file.Role)
	}
	return out
}

func packetHandles(packet Packet) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range packet.Files {
		if file.Path != "" {
			out["file://"+file.Path] = struct{}{}
		}
	}
	for _, ev := range packet.Evidence {
		if ev.Handle != "" {
			out[ev.Handle] = struct{}{}
		}
		if ev.Path != "" {
			out["file://"+ev.Path] = struct{}{}
		}
	}
	for _, item := range packet.CandidateScope {
		if item.Handle != "" {
			out[item.Handle] = struct{}{}
		}
		if item.Path != "" {
			out["file://"+item.Path] = struct{}{}
		}
	}
	if packet.Freshness != nil {
		for _, view := range packet.Freshness.ContextViews {
			if view.PackID != "" {
				out["context_pack://"+view.PackID] = struct{}{}
			}
			if view.Path != "" {
				out["file://"+view.Path] = struct{}{}
			}
		}
	}
	return out
}

func packetTestCommands(packet Packet) []string {
	out := make([]string, 0, len(packet.Tests))
	for _, test := range packet.Tests {
		out = append(out, test.Command)
	}
	sort.Strings(out)
	return out
}

func pathAllowedAsNew(path string, roles map[string]string, lines []string) bool {
	role := roles[path]
	if strings.Contains(role, "create") || strings.Contains(role, "new") || strings.Contains(role, "add") {
		return true
	}
	for _, line := range lines {
		if !strings.Contains(line, path) {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "create") || strings.Contains(lower, "new file") || strings.Contains(lower, "add ") {
			return true
		}
	}
	return false
}

func fileExistsUnderRoot(root string, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	full, ok := pathUnderRoot(root, path)
	if !ok {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && !info.IsDir()
}

func pathExistsUnderRoot(root string, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	full, ok := pathUnderRoot(root, path)
	if !ok {
		return false
	}
	_, err := os.Stat(full)
	return err == nil
}

func pathUnderRoot(root string, path string) (string, bool) {
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", false
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(cleanRoot, path)
	}
	cleanFull, err := filepath.Abs(filepath.Clean(full))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(cleanRoot, cleanFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return cleanFull, true
}

func commandReferencePaths(command string) []string {
	fields := strings.Fields(command)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, " \t\n\r'\"`")
		if token == "" || strings.HasPrefix(token, "-") || strings.Contains(token, "...") {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "./") && !strings.HasPrefix(token, "scripts/") {
			continue
		}
		token = strings.TrimPrefix(token, "./")
		if token == "" || token == "." || token == ".." || filepath.IsAbs(token) || strings.HasPrefix(token, "../") || strings.Contains(token, "://") {
			continue
		}
		if commandTokenIsWorkspaceReference(token) {
			out = append(out, token)
		}
	}
	return cleanStrings(out)
}

func commandTokenIsWorkspaceReference(token string) bool {
	if strings.HasPrefix(token, "scripts/") {
		return true
	}
	for _, prefix := range []string{"cmd/", "internal/", "pkg/", "native/", "test/", "tests/"} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	if !strings.Contains(token, "/") {
		return false
	}
	switch strings.ToLower(filepath.Ext(token)) {
	case ".bash", ".gd", ".go", ".js", ".json", ".md", ".py", ".rs", ".sh", ".ts", ".yaml", ".yml":
		return true
	}
	return false
}

func lineFor(plan string, needle string, predicate func(string) bool) bool {
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, needle) && predicate(line) {
			return true
		}
	}
	return false
}

func mentionsEditVerb(line string) bool {
	lower := strings.ToLower(line)
	for _, verb := range []string{"edit", "modify", "change", "write", "delete", "remove", "replace", "patch"} {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	return false
}

func mentionsUnresolved(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "unresolved") || strings.Contains(lower, "missing evidence") || strings.Contains(lower, "missing claim") || strings.Contains(lower, "blocked")
}

func taskSuggestsImplementation(task string) bool {
	lower := strings.ToLower(task)
	for _, word := range []string{"implement", "fix", "change", "add", "remove", "refactor", "begin on", "build"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func looksScopedCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if strings.Contains(command, "./...") {
		return false
	}
	for _, scoped := range []string{" ./", " /", " internal/", " cmd/", " pkg/", " --run ", " -run ", " TEST=", " LOG="} {
		if strings.Contains(command, scoped) {
			return true
		}
	}
	return strings.HasPrefix(command, "make ")
}

func finalizePlanAuditResult(findings []PlanAuditFinding) PlanAuditResult {
	findings = sanitizePlanAuditFindings(findings)
	ok := true
	for _, finding := range findings {
		if finding.Severity == "error" {
			ok = false
			break
		}
	}
	return PlanAuditResult{
		OK:              ok,
		MismatchMetrics: planAuditMismatchMetrics(findings),
		Findings:        findings,
	}
}

func planAuditMismatchMetrics(findings []PlanAuditFinding) PlanAuditMismatchMetrics {
	var metrics PlanAuditMismatchMetrics
	for _, finding := range findings {
		switch finding.Kind {
		case "packet_identity_mismatch", "evidence_packet_mismatch":
			metrics.PacketIdentityMismatches++
		case "packet_freshness_mismatch", "stale_fact":
			metrics.PacketFreshnessMismatches++
		case "scope_path_mismatch", "missing_file", "protected_path":
			metrics.ScopePathMismatches++
		case "evidence_scope_mismatch", "unresolved_handle":
			metrics.EvidenceMismatches++
		case "unresolved_question_mismatch", "missing_exact_gap", "unresolved_placeholder":
			metrics.UnresolvedQuestionMismatches++
		case "validation_gap", "verification_obligation_open":
			metrics.VerificationObligationSignals++
		case "approval_handoff_mismatch":
			metrics.ApprovalHandoffMismatches++
		}
	}
	return metrics
}

func packetPackIDs(packet Packet) map[string]struct{} {
	out := map[string]struct{}{}
	if packet.Freshness != nil {
		for _, view := range packet.Freshness.ContextViews {
			if view.PackID != "" {
				out[view.PackID] = struct{}{}
			}
		}
	}
	for _, ev := range packet.Evidence {
		if strings.HasPrefix(ev.Handle, "context_pack://") {
			out[strings.TrimPrefix(ev.Handle, "context_pack://")] = struct{}{}
		}
	}
	return out
}

func packetScopePaths(packet Packet) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range packet.Files {
		if file.Path != "" {
			out[file.Path] = struct{}{}
		}
	}
	for _, item := range packet.CandidateScope {
		if item.Path != "" {
			out[item.Path] = struct{}{}
		}
	}
	for _, ev := range packet.Evidence {
		if ev.Path != "" {
			out[ev.Path] = struct{}{}
		}
	}
	return out
}

func packetValidationCommands(packet Packet) []string {
	out := make([]string, 0)
	for _, test := range packet.Tests {
		if strings.TrimSpace(test.Command) != "" {
			out = append(out, test.Command)
		}
	}
	if packet.Contracts != nil {
		for _, test := range packet.Contracts.ValidationRequirements {
			if strings.TrimSpace(test.Command) != "" {
				out = append(out, test.Command)
			}
		}
	}
	out = cleanStrings(out)
	sort.Strings(out)
	return out
}

func trimPlanPathLineSuffix(path string) string {
	cleanPath := strings.TrimSuffix(path, ":")
	if idx := strings.LastIndex(cleanPath, ":"); idx > 1 && allDigits(cleanPath[idx+1:]) {
		return cleanPath[:idx]
	}
	return cleanPath
}

func artifactClaimsFresh(packet PlanArtifactPacketAuditSubject) bool {
	status := strings.ToLower(strings.TrimSpace(packet.Status))
	freshness := strings.ToLower(strings.TrimSpace(packet.Freshness))
	return status == "fresh" || freshness == "fresh"
}

func planArtifactAuditEditPaths(edits []PlanArtifactEditAuditSubject) []string {
	out := make([]string, 0, len(edits))
	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) != "" {
			out = append(out, edit.Path)
		}
	}
	return out
}

func stringSetFromStrings(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func artifactQuestionClosed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "resolved", "answered", "not_applicable", "n_a":
		return true
	default:
		return false
	}
}

func artifactVerificationClosed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed", "complete", "closed", "satisfied", "not_applicable", "n_a":
		return true
	default:
		return false
	}
}

func artifactApprovalSatisfied(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "implicit", "not_required", "n_a":
		return true
	default:
		return false
	}
}

func artifactVerificationMentionsCommand(obligations []PlanArtifactVerifyAuditSubject, command string) bool {
	for _, obligation := range obligations {
		if strings.TrimSpace(obligation.Command) == strings.TrimSpace(command) {
			return true
		}
	}
	return false
}

func sanitizePlanAuditFindings(findings []PlanAuditFinding) []PlanAuditFinding {
	out := make([]PlanAuditFinding, 0, len(findings))
	for _, finding := range findings {
		item := PlanAuditFinding{
			Severity:            sanitizePlanAuditToken(firstNonEmpty(finding.Severity, "warning")),
			Kind:                sanitizePlanAuditToken(finding.Kind),
			Message:             sanitizePlanAuditText(finding.Message, 240),
			Evidence:            sanitizePlanAuditEvidence(finding.Evidence),
			SuggestedResolution: sanitizePlanAuditText(finding.SuggestedResolution, 240),
		}
		out = append(out, item)
	}
	return out
}

func sanitizePlanAuditEvidence(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := sanitizePlanAuditText(value, 160)
		if text == "" {
			continue
		}
		out = append(out, text)
		if len(out) >= 8 {
			break
		}
	}
	return cleanStrings(out)
}

func sanitizePlanAuditText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planAuditTextLooksSensitive(value) {
		return "redacted:" + planAuditHash(value)
	}
	value = strings.Join(strings.Fields(value), " ")
	if limit > 0 && len(value) > limit {
		value = value[:limit] + "...(truncated)"
	}
	return value
}

func sanitizePlanAuditToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || planAuditTextLooksSensitive(value) {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == '.' || r == ':':
			if !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
		if b.Len() >= 120 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func planAuditTextLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"raw_prompt", "raw prompt", "raw_body", "raw body", "raw_content", "raw content",
		"prompt_body", "prompt body", "transcript", "file_body", "file body", "file-body",
		"private key", "secret", "token=", "token:", "access_token", "api token",
		"secret=", "api_key", "api-key", "password=", "bearer ", "customer-",
		"customer_", "secrets/", "/secrets/", "\\secrets\\",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if token == "token" || token == "tokens" || token == "customer" || token == "customers" {
			return true
		}
	}
	normalized := strings.ReplaceAll(lower, "\\", "/")
	for _, marker := range []string{"/users/", "/home/", "/volumes/", "/private/", "c:/users/"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func planAuditHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
