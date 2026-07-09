package planexecute

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/textutil"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/sensitivity"
	"idiom/internal/repository/comprehension"
)

func ClassifyExecutorHandoffPacket(root string, plan planartifact.PlanArtifact) ExecutorHandoffPacket {
	handoff := ExecutorHandoffPacket{
		State:                   plancontract.ExecutorHandoffStateMissing,
		Reason:                  "missing_packet_id",
		HandoffCreated:          strings.TrimSpace(plan.Handoff.ID) != "",
		PacketFreshnessState:    plancontract.ExecutorPacketFreshnessMissing,
		PlanID:                  sanitizePlanExecuteEventID(plan.PlanID),
		HandoffID:               sanitizePlanExecuteEventID(plan.Handoff.ID),
		HandoffStatus:           sanitizePlanExecuteEventToken(string(plan.Handoff.Status)),
		ApprovalScopeID:         sanitizePlanExecuteEventID(plan.Approval.ScopeID),
		ApprovalStatus:          sanitizePlanExecuteEventToken(string(plan.Approval.Status)),
		ApprovalPermission:      sanitizePlanExecuteEventToken(string(plan.Approval.ExecutionPermission)),
		ArtifactPacketStatus:    sanitizePlanExecuteEventToken(string(plan.Packet.Status)),
		ArtifactPacketFreshness: sanitizePlanExecuteEventToken(plan.Packet.Freshness),
		IndexID:                 sanitizePlanExecuteEventToken(plan.Packet.IndexID),
		ApprovedPathCount:       len(plan.Approval.ApprovedPaths),
		PendingPathCount:        len(plan.Approval.PendingPaths),
	}
	topLevelPackID, topLevelValid := normalizePlanExecuteEventPackID(plan.PackID)
	declaredPackID, declaredValid := normalizePlanExecuteEventPackID(plan.Packet.PackID)
	rawTopLevelPackID := strings.TrimSpace(plan.PackID)
	rawDeclaredPackID := strings.TrimSpace(plan.Packet.PackID)

	if rawTopLevelPackID != "" && !topLevelValid {
		handoff.Reason = "invalid_pack_id"
		return handoff
	}

	if rawDeclaredPackID != "" && !declaredValid {
		handoff.Reason = "invalid_declared_packet_id"
		if topLevelPackID == "" {
			return handoff
		}
	}

	effectivePackID := firstNonEmptyString(topLevelPackID, declaredPackID)

	handoff.PackID = effectivePackID
	handoff.DeclaredPackID = declaredPackID

	if effectivePackID == "" {
		if textutil.EqualTrimFold(string(plan.Packet.Status), string(planartifact.PacketStatusNotUsed)) {
			handoff.PacketNotUsedByDesign = true
			handoff.PacketFreshnessState = plancontract.ExecutorPacketFreshnessNotUsedByDesign
			handoff.Reason = "packet_not_used_by_design"
		}

		return handoff
	}

	overridden := topLevelPackID != "" && declaredPackID != "" && topLevelPackID != declaredPackID
	if overridden || (topLevelPackID != "" && rawDeclaredPackID != "" && !declaredValid) {
		handoff.State = plancontract.ExecutorHandoffStateOverridden
		handoff.Reason = "packet_id_override"
	}

	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, effectivePackID)
	if err != nil {
		if handoff.State != plancontract.ExecutorHandoffStateOverridden {
			handoff.State = plancontract.ExecutorHandoffStateMissing
			handoff.Reason = "cache_load_error"
		}

		return handoff
	}

	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = plancontract.ExecutorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else if handoff.Fresh {
			handoff.State = plancontract.ExecutorHandoffStateInherited
			handoff.Reason = "fresh_graph_view"
		} else {
			handoff.State = plancontract.ExecutorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}

		return handoff
	case comprehension.ViewLoadStateStale:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = plancontract.ExecutorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else if handoff.Fresh {
			handoff.State = plancontract.ExecutorHandoffStateInherited
			handoff.Reason = "fresh_scoped_graph_view"
		} else {
			handoff.State = plancontract.ExecutorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}

		return handoff
	}

	if handoff.State != plancontract.ExecutorHandoffStateOverridden {
		handoff.State = plancontract.ExecutorHandoffStateMissing
		handoff.Reason = "cache_miss"
	}

	return handoff
}

func applyExecutorHandoffView(handoff ExecutorHandoffPacket, view comprehension.PackResult) ExecutorHandoffPacket {
	handoff.PackID = firstNonEmptyString(normalizePlanExecutePackID(view.PackID), handoff.PackID)
	handoff.IndexID = sanitizePlanExecuteEventToken(firstNonEmptyString(view.IndexID, handoff.IndexID))
	handoff.Fresh = executorHandoffViewFresh(view)
	handoff.PacketPresent = true

	if handoff.Fresh {
		handoff.PacketFreshnessState = plancontract.ExecutorPacketFreshnessFresh
	} else {
		handoff.PacketFreshnessState = plancontract.ExecutorPacketFreshnessStale
	}

	handoff.CandidateFiles = len(view.CandidateFiles)
	handoff.CandidateSymbols = len(view.CandidateSymbols)
	handoff.Tests = len(view.Tests)

	proj := comprehension.NewCompactPackProjection(view)

	handoff.Quality = packQualityFromComprehension(proj.Quality)

	return handoff
}

func executorHandoffViewFresh(view comprehension.PackResult) bool {
	return view.Fresh || (view.Freshness != nil && view.Freshness.FreshForScope)
}

func emitExecutorHandoffPacketEvents(stdout io.Writer, jsonEvents bool, handoff ExecutorHandoffPacket) error {
	fields := executorHandoffPacketEventFields(handoff)
	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorHandoffPacket, fields); err != nil {
		return planExecuteJSONEventWriteError(err)
	}

	switch handoff.State {
	case plancontract.ExecutorHandoffStateInherited:
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorInheritsPack, fields); err != nil {
			return planExecuteJSONEventWriteError(err)
		}

		if !jsonEvents && handoff.PackID != "" {
			fmt.Fprintf(stdout, "  executor inherits planner pack %s (%d files, %d symbols, %d tests)\n",
				handoff.PackID, handoff.CandidateFiles, handoff.CandidateSymbols, handoff.Tests)
		}
	case plancontract.ExecutorHandoffStateMissing:
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorPackMiss, fields); err != nil {
			return planExecuteJSONEventWriteError(err)
		}
	case plancontract.ExecutorHandoffStateStale:
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorPacketStale, fields); err != nil {
			return planExecuteJSONEventWriteError(err)
		}
	case plancontract.ExecutorHandoffStateOverridden:
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorPacketOverridden, fields); err != nil {
			return planExecuteJSONEventWriteError(err)
		}
	}

	return nil
}

func executorHandoffPacketEventFields(handoff ExecutorHandoffPacket) map[string]any {
	fields := map[string]any{
		plancontract.PlanExecuteEventFieldPacketState:           handoff.State,
		plancontract.PlanExecuteEventFieldReason:                handoff.Reason,
		plancontract.PlanExecuteEventFieldFresh:                 handoff.Fresh,
		plancontract.PlanExecuteEventFieldHandoffCreated:        handoff.HandoffCreated,
		plancontract.PlanExecuteEventFieldPacketPresent:         handoff.PacketPresent,
		plancontract.PlanExecuteEventFieldPacketNotUsedByDesign: handoff.PacketNotUsedByDesign,
		plancontract.PlanExecuteEventFieldPacketFreshnessState:  firstNonEmptyString(string(handoff.PacketFreshnessState), string(plancontract.ExecutorPacketFreshnessUnknown)),
	}
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldPlanID, handoff.PlanID)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldHandoffID, handoff.HandoffID)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldHandoffStatus, handoff.HandoffStatus)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldApprovalScopeID, handoff.ApprovalScopeID)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldApprovalStatus, handoff.ApprovalStatus)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldApprovalExecutionPermission, handoff.ApprovalPermission)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldPackID, handoff.PackID)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldDeclaredPackID, handoff.DeclaredPackID)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldArtifactPacketStatus, handoff.ArtifactPacketStatus)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldArtifactPacketFreshness, handoff.ArtifactPacketFreshness)
	addPlanExecuteEventString(fields, plancontract.PlanExecuteEventFieldIndexID, handoff.IndexID)

	if handoff.ApprovedPathCount > 0 {
		fields["approved_path_count"] = handoff.ApprovedPathCount
	}

	if handoff.PendingPathCount > 0 {
		fields["pending_path_count"] = handoff.PendingPathCount
	}

	if handoff.CandidateFiles > 0 {
		fields["candidate_files"] = handoff.CandidateFiles
	}

	if handoff.CandidateSymbols > 0 {
		fields["candidate_symbols"] = handoff.CandidateSymbols
	}

	if handoff.Tests > 0 {
		fields["tests"] = handoff.Tests
	}

	quality := executorHandoffQualityFields(handoff.Quality)
	if len(quality) > 0 {
		fields[plancontract.PlanExecuteEventFieldQuality] = quality
	}

	return fields
}

func executorHandoffQualityFields(q PackQuality) map[string]any {
	confidence := strings.TrimSpace(q.LocalizationConfidence)
	if confidence == "" {
		return nil
	}

	fields := map[string]any{
		plancontract.PlanExecuteEventFieldLocalizationConfidence: confidence,
		plancontract.PlanExecuteEventFieldSourceOwnerFound:       q.SourceOwnerFound,
	}
	if len(q.EvidencePaths) > 0 {
		fields[plancontract.PlanExecuteEventFieldEvidencePaths] = q.EvidencePaths
	}

	if len(q.Reasons) > 0 {
		fields[plancontract.PlanExecuteEventFieldReasons] = q.Reasons
	}

	return fields
}

func packQualityFromComprehension(q comprehension.CompactPackQuality) PackQuality {
	return PackQuality{
		LocalizationConfidence: strings.TrimSpace(q.LocalizationConfidence),
		SourceOwnerFound:       q.SourceOwnerFound,
		EvidencePaths:          append([]string(nil), q.EvidencePaths...),
		Reasons:                append([]string(nil), q.Reasons...),
	}
}

func addPlanExecuteEventString(fields map[string]any, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		fields[key] = value
	}
}

func normalizePlanExecuteEventPackID(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	normalized := normalizePlanExecutePackID(raw)

	if normalized == "" {
		return "", raw == ""
	}

	return normalized, true
}

func normalizePlanExecutePackID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if isPlanExecutePackID(value) {
		return value
	}

	return ""
}

func sanitizePlanExecuteEventID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if sensitivity.ContainsSensitiveText(value) {
		return "redacted:" + planExecuteEventHash(value)
	}

	return sanitizePlanExecuteEventToken(value)
}

func sanitizePlanExecuteEventToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
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

func sanitizePlanExecuteEventPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if sensitivity.ContainsSensitiveText(value) {
		return "redacted:" + planExecuteEventHash(value)
	}

	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	value = strings.TrimPrefix(value, "./")

	if value == "." {
		return ""
	}

	return value
}

func planExecuteEventHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
