package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/sensitivity"
	"idiom/internal/platform/textutil"
)

func newPlanArtifactFromSubmitPlanRequest(req ToolRequest, sessionID string, created string, planID string) (planartifact.PlanArtifact, error) {
	if req.PlanArtifact != nil {
		artifact := *req.PlanArtifact
		if len(artifact.Edits) == 0 && len(req.Edits) > 0 {
			artifact.Edits = append([]planartifact.PlanEdit(nil), req.Edits...)
		}

		if len(artifact.Paths.Required) == 0 && len(artifact.Contract.RequiredEditPaths) > 0 {
			artifact.Paths.Required = append([]string(nil), artifact.Contract.RequiredEditPaths...)
		}

		artifact.Paths.SourceSatisfied = append(append([]string(nil), artifact.Paths.SourceSatisfied...), req.SourceSatisfiedPaths...)
		artifact.Edits = planEditsWithoutAnchorProvenance(artifact.Edits)

		if strings.TrimSpace(artifact.Rationale) == "" {
			artifact.Rationale = req.Rationale
		}

		if strings.TrimSpace(artifact.PackID) == "" {
			artifact.PackID = req.PackID
		}

		artifact = planartifact.NormalizeForSubmit(artifact, planartifact.NormalizeForSubmitOptions{
			SessionID: sessionID,
			Created:   created,
			PlanID:    planID,
		})
		if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
			return planartifact.PlanArtifact{}, err
		}

		return artifact, nil
	}

	artifact := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        planID,
		Created:       created,
		Rationale:     sanitizePlanArtifactText(req.Rationale, 2000),
		PackID:        strings.TrimSpace(req.PackID),
		SessionID:     strings.TrimSpace(sessionID),
		Edits:         planEditsWithoutAnchorProvenance(req.Edits),
		Task: planartifact.PlanArtifactTask{
			Summary: sanitizePlanArtifactText(textutil.FirstNonEmpty(req.Rationale, "legacy submit_plan request"), 240),
		},
		Contract: planartifact.PlanArtifactContract{
			Kind:              planartifact.ContractKindPlanThenExecute,
			StrategyOwner:     planartifact.OwnerModel,
			ContractOwner:     planartifact.OwnerHarness,
			PermissionProfile: planartifact.PermissionProfilePlanThenExecute,
			VerificationOwner: planartifact.OwnerPlanner,
		},
		Packet: planartifact.PlanArtifactPacket{
			PackID: strings.TrimSpace(req.PackID),
		},
		Paths: planartifact.PlanArtifactPaths{
			SourceSatisfied: req.SourceSatisfiedPaths,
		},
		Approval: planartifact.PlanArtifactApproval{
			Required:         false,
			Status:           planartifact.ApprovalStatusImplicit,
			ScopeID:          planID + ":submit_plan",
			Mode:             planartifact.ApprovalModePlanThenExecute,
			ExpirationPolicy: planartifact.ApprovalExpirationWorkspaceDigest,
		},
		Handoff: planartifact.PlanArtifactHandoff{
			ID:     planID + ":handoff",
			From:   planartifact.OwnerPlanner,
			To:     planartifact.HandoffTargetPlanExecuteExecutor,
			Status: planartifact.HandoffStatusReady,
			Ready:  true,
		},
	}

	artifact = planartifact.NormalizeForSubmit(artifact, planartifact.NormalizeForSubmitOptions{
		SessionID:     sessionID,
		Created:       created,
		PlanID:        planID,
		LegacyRequest: true,
	})

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		return planartifact.PlanArtifact{}, err
	}

	return artifact, nil
}

func planEditsWithoutAnchorProvenance(edits []planartifact.PlanEdit) []planartifact.PlanEdit {
	out := append([]planartifact.PlanEdit(nil), edits...)
	for i := range out {
		out[i].AnchorProvenance = nil
	}

	return out
}

func sanitizePlanArtifactPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || sensitivity.ContainsSensitiveText(value) {
		return ""
	}

	value = strings.ReplaceAll(value, "\\", "/")
	if !planartifact.PathIsSafe(value) {
		return ""
	}

	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	value = strings.TrimPrefix(value, "./")

	if value == "." {
		return ""
	}

	return sanitizePlanArtifactVisibleText(value, 240)
}

func sanitizePlanArtifactID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || sensitivity.ContainsSensitiveText(value) {
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

func sanitizePlanArtifactToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || sensitivity.ContainsSensitiveText(value) {
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

func sanitizePlanArtifactText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if sensitivity.ContainsSensitiveText(value) {
		return "redacted:" + planArtifactHash(value)
	}

	return sanitizePlanArtifactVisibleText(value, maxBytes)
}

func sanitizePlanArtifactVisibleText(value string, maxBytes int) string {
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

func planArtifactHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
