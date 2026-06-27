package planartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"idiom/internal/contracts/sensitivity"
)

type NormalizeForSubmitOptions struct {
	SessionID     string
	Created       string
	PlanID        string
	LegacyRequest bool
}

func NormalizeForSubmit(artifact PlanArtifact, opts NormalizeForSubmitOptions) PlanArtifact {
	switch schemaVersion := strings.TrimSpace(artifact.SchemaVersion); schemaVersion {
	case "", PreviousSchemaVersion:
		artifact.SchemaVersion = SchemaVersion
	default:
		artifact.SchemaVersion = schemaVersion
	}

	artifact.PlanID = sanitizeID(firstNonEmpty(artifact.PlanID, opts.PlanID))
	artifact.Created = firstNonEmpty(strings.TrimSpace(artifact.Created), opts.Created)
	artifact.SessionID = sanitizeID(firstNonEmpty(artifact.SessionID, opts.SessionID))
	artifact.Rationale = sanitizeText(artifact.Rationale, 2000)
	artifact.Task.ID = sanitizeID(artifact.Task.ID)
	artifact.Task.Summary = sanitizeText(artifact.Task.Summary, 240)

	if opts.LegacyRequest && strings.TrimSpace(artifact.Task.Summary) == "" {
		artifact.Task.Summary = sanitizeText(firstNonEmpty(artifact.Rationale, "legacy submit_plan request"), 240)
	}

	artifact.Task.ClientClassifications = normalizeClientClassifications(artifact.Task.ClientClassifications)
	artifact.Contract.Kind = ContractKind(sanitizeToken(string(artifact.Contract.Kind)))
	artifact.Contract.StrategyOwner = Owner(sanitizeToken(string(artifact.Contract.StrategyOwner)))
	artifact.Contract.ContractOwner = Owner(sanitizeToken(string(artifact.Contract.ContractOwner)))
	artifact.Contract.PermissionProfile = sanitizeToken(artifact.Contract.PermissionProfile)
	artifact.Contract.VerificationOwner = Owner(sanitizeToken(string(artifact.Contract.VerificationOwner)))

	if opts.LegacyRequest {
		artifact.Contract.Kind = ContractKind(firstNonEmpty(string(artifact.Contract.Kind), string(ContractKindPlanThenExecute)))
		artifact.Contract.StrategyOwner = Owner(firstNonEmpty(string(artifact.Contract.StrategyOwner), string(OwnerModel)))
		artifact.Contract.ContractOwner = Owner(firstNonEmpty(string(artifact.Contract.ContractOwner), string(OwnerHarness)))
		artifact.Contract.PermissionProfile = firstNonEmpty(artifact.Contract.PermissionProfile, PermissionProfilePlanThenExecute)
		artifact.Contract.VerificationOwner = Owner(firstNonEmpty(string(artifact.Contract.VerificationOwner), string(OwnerPlanner)))
	}

	artifact.Contract.RequiredEditPaths = NormalizePaths(artifact.Contract.RequiredEditPaths)
	artifact.Contract.ForbiddenPaths = NormalizePaths(artifact.Contract.ForbiddenPaths)

	if strings.TrimSpace(artifact.PackID) == "" {
		artifact.PackID = strings.TrimSpace(artifact.Packet.PackID)
	}
	if strings.TrimSpace(artifact.Packet.PackID) == "" {
		artifact.Packet.PackID = strings.TrimSpace(artifact.PackID)
	}

	artifact.PackID = SanitizePackID(artifact.PackID)
	artifact.Packet.PackID = SanitizePackID(artifact.Packet.PackID)
	if strings.TrimSpace(string(artifact.Packet.Status)) == "" {
		if strings.TrimSpace(artifact.Packet.PackID) == "" {
			artifact.Packet.Status = PacketStatusNotProvided
		} else {
			artifact.Packet.Status = PacketStatusProvided
		}
	}
	artifact.Packet.Status = PacketStatus(sanitizeToken(string(artifact.Packet.Status)))
	artifact.Packet.IndexID = sanitizeToken(artifact.Packet.IndexID)
	artifact.Packet.Freshness = sanitizeToken(artifact.Packet.Freshness)
	artifact.Packet.ArtifactPath = sanitizePathValue(artifact.Packet.ArtifactPath)
	artifact.Edits = normalizeEdits(artifact.Edits)

	editPaths := EditPaths(artifact.Edits)
	artifact.Paths.Editable = NormalizePaths(firstNonEmptyStrings(artifact.Paths.Editable, editPaths))
	artifact.Paths.Changed = NormalizePaths(firstNonEmptyStrings(artifact.Paths.Changed, editPaths))
	artifact.Paths.Required = NormalizePaths(firstNonEmptyStrings(artifact.Paths.Required, artifact.Contract.RequiredEditPaths))
	artifact.Paths.SourceSatisfied = NormalizePaths(artifact.Paths.SourceSatisfied)
	artifact.Paths.ReadOnlyEvidence = NormalizePaths(artifact.Paths.ReadOnlyEvidence)
	artifact.Paths.Forbidden = NormalizePaths(firstNonEmptyStrings(artifact.Paths.Forbidden, artifact.Contract.ForbiddenPaths))
	artifact.Evidence = normalizeEvidence(artifact.Evidence)
	if len(artifact.Evidence) == 0 {
		artifact.Evidence = defaultEvidence(artifact)
	}

	artifact.UnresolvedQuestions = normalizeQuestions(artifact.UnresolvedQuestions)
	artifact.VerificationObligations = normalizeVerification(artifact.VerificationObligations)
	if len(artifact.VerificationObligations) == 0 && opts.LegacyRequest {
		artifact.VerificationObligations = []PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     VerificationKindExecutorLaneSanity,
			Owner:    Owner(firstNonEmpty(string(artifact.Contract.VerificationOwner), string(OwnerPlanner))),
			Status:   VerificationStatusOpen,
			Required: true,
		}}
	}

	artifact.Approval.Status = ApprovalStatus(sanitizeToken(firstNonEmpty(string(artifact.Approval.Status), string(ApprovalStatusImplicit))))
	artifact.Approval.ScopeID = sanitizeID(artifact.Approval.ScopeID)
	artifact.Approval.Mode = ApprovalMode(sanitizeToken(string(artifact.Approval.Mode)))
	artifact.Approval.ApprovedBy = sanitizeID(artifact.Approval.ApprovedBy)
	artifact.Approval.ApprovedAt = sanitizeToken(artifact.Approval.ApprovedAt)
	artifact.Approval.ExpirationPolicy = ApprovalExpirationPolicy(sanitizeToken(string(artifact.Approval.ExpirationPolicy)))
	artifact.Approval.ApprovedPaths = NormalizePaths(artifact.Approval.ApprovedPaths)
	artifact.Approval.PendingPaths = NormalizePaths(artifact.Approval.PendingPaths)
	artifact.Approval.ExecutionPermission = ExecutionPermission(sanitizeToken(string(artifact.Approval.ExecutionPermission)))

	if artifact.Approval.Required && ApprovalStatusSatisfied(string(artifact.Approval.Status)) && len(artifact.Approval.ApprovedPaths) == 0 {
		artifact.Approval.ApprovedPaths = NormalizePaths(firstNonEmptyStrings(artifact.Paths.Required, editPaths))
	}
	if artifact.Approval.ScopeID == "" {
		artifact.Approval.ScopeID = stableApprovalScopeID(artifact, editPaths)
	}
	if artifact.Approval.ExecutionPermission == "" && !IsApprovalStatusPartial(string(artifact.Approval.Status)) {
		artifact.Approval.ExecutionPermission = defaultExecutionPermission(artifact)
	}

	artifact.Handoff.ID = sanitizeID(artifact.Handoff.ID)
	if artifact.Handoff.ID == "" {
		artifact.Handoff.ID = stableHandoffID(artifact, editPaths)
	}
	artifact.Handoff.From = Owner(sanitizeToken(string(artifact.Handoff.From)))
	artifact.Handoff.To = HandoffTarget(sanitizeToken(string(artifact.Handoff.To)))
	artifact.Handoff.Status = HandoffStatus(sanitizeToken(firstNonEmpty(string(artifact.Handoff.Status), string(HandoffStatusReady))))
	artifact.Handoff.Blockers = normalizeTokens(artifact.Handoff.Blockers)

	if opts.LegacyRequest {
		artifact.Handoff.From = Owner(firstNonEmpty(string(artifact.Handoff.From), string(OwnerPlanner)))
		artifact.Handoff.To = HandoffTarget(firstNonEmpty(string(artifact.Handoff.To), string(HandoffTargetPlanExecuteExecutor)))
		if len(artifact.Handoff.Blockers) == 0 {
			artifact.Handoff.Ready = true
		}
	}
	if !artifact.Handoff.Ready && handoffExecutable(artifact) {
		artifact.Handoff.Ready = true
	}
	if !artifact.Handoff.Ready {
		artifact.Handoff.Status = HandoffStatusBlocked
		if len(artifact.Handoff.Blockers) == 0 {
			artifact.Handoff.Blockers = []string{handoffBlockedReason(artifact)}
		}
	}

	artifact.LegacyProse = sanitizeText(artifact.LegacyProse, 2000)

	return artifact
}

func NormalizeLoadedPlanArtifact(artifact PlanArtifact) PlanArtifact {
	loadedSchemaVersion := strings.TrimSpace(artifact.SchemaVersion)
	if loadedSchemaVersion == LegacySchemaVersion || loadedSchemaVersion == PreviousSchemaVersion {
		artifact.Edits = MarkLegacyDeletionEditsExplicit(artifact.Edits)
	}

	if loadedSchemaVersion == LegacySchemaVersion {
		artifact.SchemaVersion = LegacySchemaVersion
		return NormalizeForSubmit(artifact, NormalizeForSubmitOptions{
			SessionID:     artifact.SessionID,
			Created:       artifact.Created,
			PlanID:        firstNonEmpty(artifact.PlanID, "legacy-plan"),
			LegacyRequest: true,
		})
	}

	artifact = NormalizeForSubmit(artifact, NormalizeForSubmitOptions{
		SessionID: artifact.SessionID,
		Created:   artifact.Created,
		PlanID:    artifact.PlanID,
	})
	if loadedSchemaVersion == PreviousSchemaVersion {
		artifact.SchemaVersion = PreviousSchemaVersion
	}

	return artifact
}

func EditPaths(edits []PlanEdit) []string {
	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		if path := strings.TrimSpace(edit.Path); path != "" {
			paths = append(paths, path)
		}
	}

	return NormalizePaths(paths)
}

func NormalizePaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		path := sanitizePathValue(value)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	slices.Sort(out)

	return out
}

func SanitizePackID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 16 {
		return ""
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}

	return value
}

func normalizeEdits(edits []PlanEdit) []PlanEdit {
	out := append([]PlanEdit(nil), edits...)
	for i := range out {
		out[i].Purpose = sanitizeEditPurpose(out[i].Purpose, 240)
	}

	return out
}

func normalizeEvidence(values []PlanArtifactEvidence) []PlanArtifactEvidence {
	out := make([]PlanArtifactEvidence, 0, len(values))
	for i, value := range values {
		item := PlanArtifactEvidence{
			ID:         sanitizeID(firstNonEmpty(value.ID, fmt.Sprintf("evidence-%d", i+1))),
			Kind:       EvidenceKind(sanitizeToken(string(value.Kind))),
			Handle:     sanitizeHandle(value.Handle),
			Path:       sanitizePathValue(value.Path),
			Summary:    sanitizeText(value.Summary, 240),
			Freshness:  sanitizeToken(value.Freshness),
			Confidence: sanitizeToken(value.Confidence),
		}
		if item.Kind == "" && item.Handle == "" && item.Path == "" && item.Summary == "" {
			continue
		}
		out = append(out, item)
	}

	return out
}

func normalizeQuestions(values []PlanArtifactQuestion) []PlanArtifactQuestion {
	out := make([]PlanArtifactQuestion, 0, len(values))
	for i, value := range values {
		item := PlanArtifactQuestion{
			ID:                    sanitizeID(firstNonEmpty(value.ID, fmt.Sprintf("question-%d", i+1))),
			Claim:                 sanitizeText(value.Claim, 240),
			Probe:                 sanitizeText(value.Probe, 240),
			Status:                sanitizeToken(value.Status),
			RequiredBeforeHandoff: value.RequiredBeforeHandoff,
		}
		if item.Claim == "" && item.Probe == "" {
			continue
		}
		out = append(out, item)
	}

	return out
}

func normalizeVerification(values []PlanArtifactVerificationObligation) []PlanArtifactVerificationObligation {
	out := make([]PlanArtifactVerificationObligation, 0, len(values))
	for i, value := range values {
		item := PlanArtifactVerificationObligation{
			ID:       sanitizeID(firstNonEmpty(value.ID, fmt.Sprintf("verification-%d", i+1))),
			Kind:     VerificationKind(sanitizeToken(string(value.Kind))),
			Command:  sanitizeText(value.Command, 240),
			Path:     sanitizePathValue(value.Path),
			Owner:    Owner(sanitizeToken(string(value.Owner))),
			Status:   VerificationStatus(firstNonEmpty(sanitizeToken(string(value.Status)), string(VerificationStatusOpen))),
			Required: value.Required,
		}
		if item.Kind == "" && item.Command == "" && item.Path == "" {
			continue
		}
		out = append(out, item)
	}

	return out
}

func normalizeClientClassifications(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeClientClassification(value)
		if normalized == "" {
			if strings.TrimSpace(value) == "" {
				continue
			}
			normalized = sanitizeToken(value)
			if normalized == "" {
				normalized = "invalid_client_classification"
			}
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func normalizeClientClassification(value string) string {
	normalized := sanitizeToken(value)
	switch normalized {
	case "finance", "financial_services":
		return "finance"
	case "healthcare", "healthcare_life_sciences", "life_sciences":
		return "healthcare_life_sciences"
	case "public_sector", "government", "gov":
		return "public_sector"
	case "enterprise", "enterprise_saas", "saas":
		return "enterprise_saas"
	case "tooling_vendor", "tool_vendor", "developer_tooling":
		return "tooling_vendor"
	case "local_model_private_code", "private_code", "local_model":
		return "local_model_private_code"
	case "open_source", "oss":
		return "open_source"
	default:
		return ""
	}
}

func defaultEvidence(artifact PlanArtifact) []PlanArtifactEvidence {
	if strings.TrimSpace(artifact.Packet.PackID) != "" {
		return []PlanArtifactEvidence{{
			ID:        "evidence-1",
			Kind:      EvidenceKindContextPacket,
			Handle:    "context_pack://" + artifact.Packet.PackID,
			Freshness: firstNonEmpty(artifact.Packet.Freshness, string(artifact.Packet.Status)),
		}}
	}

	return []PlanArtifactEvidence{{
		ID:      "evidence-1",
		Kind:    EvidenceKindPlannerSubmission,
		Summary: "legacy submit_plan payload normalized into durable artifact",
	}}
}

func defaultExecutionPermission(artifact PlanArtifact) ExecutionPermission {
	status := strings.TrimSpace(string(artifact.Approval.Status))
	switch {
	case IsApprovalStatusPartial(status):
		return ""
	case ApprovalStatusSatisfied(status), !artifact.Approval.Required:
		return ExecutionPermissionExecuteAll
	default:
		return ExecutionPermissionBlocked
	}
}

func handoffExecutable(artifact PlanArtifact) bool {
	if strings.TrimSpace(string(artifact.Handoff.Status)) != string(HandoffStatusReady) || len(artifact.Handoff.Blockers) > 0 {
		return false
	}
	switch strings.TrimSpace(string(artifact.Approval.ExecutionPermission)) {
	case string(ExecutionPermissionExecuteAll), string(ExecutionPermissionExecuteApprovedScope):
		return true
	default:
		return false
	}
}

func handoffBlockedReason(artifact PlanArtifact) string {
	switch {
	case strings.TrimSpace(string(artifact.Approval.ExecutionPermission)) == "":
		return "execution_permission_missing"
	case artifact.Approval.ExecutionPermission == ExecutionPermissionBlocked:
		return "execution_permission_blocked"
	case artifact.Approval.Required && !ApprovalStatusSatisfied(string(artifact.Approval.Status)):
		return "approval_not_satisfied"
	default:
		return "handoff_not_executable"
	}
}

func stableApprovalScopeID(artifact PlanArtifact, editPaths []string) string {
	approved := artifact.Approval.ApprovedPaths
	if len(approved) == 0 {
		approved = firstNonEmptyStrings(artifact.Paths.Required, editPaths)
	}

	parts := []string{
		"plan:" + artifact.PlanID,
		"packet:" + artifact.Packet.PackID,
		"approval:" + string(artifact.Approval.Status),
	}
	for _, path := range NormalizePaths(approved) {
		parts = append(parts, "approved:"+path)
	}
	for _, path := range NormalizePaths(artifact.Approval.PendingPaths) {
		parts = append(parts, "pending:"+path)
	}

	return stableID("scope", parts...)
}

func stableHandoffID(artifact PlanArtifact, editPaths []string) string {
	parts := []string{
		"plan:" + artifact.PlanID,
		"scope:" + artifact.Approval.ScopeID,
		"packet:" + artifact.Packet.PackID,
		"from:" + string(artifact.Handoff.From),
		"to:" + string(artifact.Handoff.To),
	}
	for _, path := range NormalizePaths(editPaths) {
		parts = append(parts, "edit:"+path)
	}

	return stableID("handoff", parts...)
}

func stableID(prefix string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "empty")
	}

	return sanitizeID(prefix + "-" + hash(strings.Join(parts, "\x00")))
}

func normalizeTokens(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		token := sanitizeToken(value)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}

	return out
}

func firstNonEmptyStrings(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}

	return fallback
}

func sanitizePathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || textLooksSensitive(value) {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if !PathIsSafe(value) {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}

	return sanitizeVisibleText(value, 240)
}

func sanitizeHandle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || textLooksSensitive(value) {
		return ""
	}

	return sanitizeVisibleText(value, 240)
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || textLooksSensitive(value) {
		return ""
	}

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		}
		if b.Len() >= 120 {
			break
		}
	}

	return strings.Trim(b.String(), "-_.:")
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || textLooksSensitive(value) {
		return ""
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == '.' || r == ':' || r == '/':
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

func sanitizeText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if textLooksSensitive(value) {
		return "redacted:" + hash(value)
	}

	return sanitizeVisibleText(value, maxBytes)
}

func sanitizeEditPurpose(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if textLooksSensitive(value) || editPurposeLooksSensitive(value) {
		return "redacted:" + hash(value)
	}

	return sanitizeVisibleText(value, maxBytes)
}

func sanitizeVisibleText(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes && len(value) > 0 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}

	return strings.TrimSpace(value) + " [truncated]"
}

func textLooksSensitive(value string) bool {
	return sensitivity.ContainsSensitiveText(value)
}

func editPurposeLooksSensitive(value string) bool {
	return sensitivity.ContainsSensitiveText(value)
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
