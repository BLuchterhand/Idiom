package planexecute

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/repository/comprehension"
)

func collectPlanExecutePhaseEvidence(root string, phase string, plan planartifact.PlanArtifact, changedPaths []string, requiredPaths []string, allowedPaths []string, reviewEvidence ReviewRepairEvidence, executorReport ExecutorPhaseReport, coversSourceSanity SourceSanityCoverage) PhaseEvidenceBundle {
	coversSourceSanity = normalizeDependencies(Dependencies{SourceSanityCovers: coversSourceSanity}).SourceSanityCovers
	handoff := executorReport.Handoff
	if strings.TrimSpace(string(handoff.State)) == "" {
		handoff = ClassifyExecutorHandoffPacket(root, plan)
	}

	packet := loadPlanExecutePacketEvidence(root, handoff.PackID)
	submittedPlan := collectSubmittedPlanState(root, plan)

	sourceSanity := append([]plancontract.PhaseEvidenceCheck(nil), executorReport.SourceSanity...)
	patchHygiene := plancontract.PhaseEvidenceCheck{
		Kind:   plancontract.PhaseEvidenceKindPatchHygiene,
		Status: plancontract.PhaseEvidenceStatusPassed,
	}

	if reviewEvidence.DiffCheckExit != 0 {
		patchHygiene.Status = plancontract.PhaseEvidenceStatusFailed
		if reviewEvidence.DiffCheckExit != 1 {
			patchHygiene.Status = plancontract.PhaseEvidenceStatusUnavailable
		}

		patchHygiene.Error = strings.TrimSpace(reviewEvidence.DiffCheck)
	}

	bundle := PhaseEvidenceBundle{
		SchemaVersion:    PhaseEvidenceSchemaVersion,
		Phase:            phaseEvidencePhaseFromString(phase),
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		ChangedPaths:     mergePlanExecutePaths(changedPaths),
		RequiredPaths:    mergePlanExecutePaths(requiredPaths),
		AllowedPaths:     mergePlanExecutePaths(allowedPaths),
		Packet:           packet,
		ExecutorHandoff:  phaseEvidenceHandoffFromExecutor(handoff),
		SubmittedPlan:    submittedPlan,
		SourceSanity:     sourceSanity,
		Verification:     append([]plancontract.PhaseEvidenceCheck(nil), executorReport.Verification...),
		PatchHygiene:     patchHygiene,
		SuggestedTests:   packetSuggestedTests(packet),
		UnresolvedGaps:   append([]string(nil), packet.UnresolvedGaps...),
		OpenObligations:  phaseEvidenceOpenObligations(plan, packet, changedPaths, requiredPaths, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene, coversSourceSanity),
		DecisionEvidence: phaseEvidenceDecisionSummary(packet, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene),
	}

	return bundle
}

func phaseEvidencePhaseFromString(raw string) PhaseEvidencePhase {
	if phase, ok := plancontract.ParsePlanExecuteProgressPhase(raw); ok {
		return PhaseEvidencePhase(phase)
	}

	return PhaseEvidencePhaseUnknown
}

func phaseEvidenceHandoffState(state plancontract.ExecutorHandoffState) plancontract.ExecutorHandoffState {
	switch state {
	case plancontract.ExecutorHandoffStateInherited,
		plancontract.ExecutorHandoffStateMissing,
		plancontract.ExecutorHandoffStateStale,
		plancontract.ExecutorHandoffStateOverridden:
		return state
	default:
		return plancontract.ExecutorHandoffStateMissing
	}
}

func phaseEvidencePacketFreshness(state plancontract.ExecutorPacketFreshnessState) plancontract.ExecutorPacketFreshnessState {
	switch state {
	case plancontract.ExecutorPacketFreshnessFresh,
		plancontract.ExecutorPacketFreshnessStale,
		plancontract.ExecutorPacketFreshnessMissing,
		plancontract.ExecutorPacketFreshnessNotUsedByDesign,
		plancontract.ExecutorPacketFreshnessUnknown:
		return state
	default:
		return plancontract.ExecutorPacketFreshnessUnknown
	}
}

func phaseEvidenceHandoffStatus(status string) planartifact.HandoffStatus {
	status = strings.TrimSpace(status)
	if planartifact.KnownHandoffStatus(status) {
		return planartifact.HandoffStatus(status)
	}

	return ""
}

func phaseEvidenceHandoffFromExecutor(handoff ExecutorHandoffPacket) phaseEvidenceHandoff {
	return phaseEvidenceHandoff{
		PacketState:      phaseEvidenceHandoffState(handoff.State),
		Reason:           strings.TrimSpace(handoff.Reason),
		HandoffCreated:   handoff.HandoffCreated,
		PacketPresent:    handoff.PacketPresent,
		PacketNotUsed:    handoff.PacketNotUsedByDesign,
		PacketFreshness:  phaseEvidencePacketFreshness(handoff.PacketFreshnessState),
		PlanID:           strings.TrimSpace(handoff.PlanID),
		HandoffID:        strings.TrimSpace(handoff.HandoffID),
		HandoffStatus:    phaseEvidenceHandoffStatus(handoff.HandoffStatus),
		PackID:           strings.TrimSpace(handoff.PackID),
		DeclaredPackID:   strings.TrimSpace(handoff.DeclaredPackID),
		IndexID:          strings.TrimSpace(handoff.IndexID),
		Fresh:            handoff.Fresh,
		Quality:          handoff.Quality,
		CandidateFiles:   handoff.CandidateFiles,
		CandidateSymbols: handoff.CandidateSymbols,
		Tests:            handoff.Tests,
	}
}

func collectSubmittedPlanState(root string, plan planartifact.PlanArtifact) []plancontract.PhaseEvidenceCheck {
	out := make([]plancontract.PhaseEvidenceCheck, 0, len(plan.Edits))
	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		check := plancontract.PhaseEvidenceCheck{
			Kind:   plancontract.PhaseEvidenceKindSubmittedEdit,
			Path:   path,
			Status: plancontract.PhaseEvidenceStatusUnknown,
		}

		if path == "" {
			check.Status = plancontract.PhaseEvidenceStatusFailed
			check.Error = "edit has no path"
			out = append(out, check)

			continue
		}

		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(strings.TrimSpace(root), path)
		}

		body, err := os.ReadFile(filepath.Clean(fullPath))
		if err != nil {
			check.Status = plancontract.PhaseEvidenceStatusFailed
			check.Error = err.Error()
			out = append(out, check)

			continue
		}

		status, summary := submittedPlanEditStatus(string(body), edit)

		check.Status = status
		check.Summary = fmt.Sprintf("edit %d/%d: %s", i+1, len(plan.Edits), summary)
		out = append(out, check)
	}

	return out
}

func submittedPlanEditStatus(current string, edit planartifact.PlanEdit) (plancontract.PhaseEvidenceStatus, string) {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		if current == edit.NewText {
			return plancontract.PhaseEvidenceStatusApplied, "whole-file content matches submitted new_text"
		}

		if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && current == repaired {
			return plancontract.PhaseEvidenceStatusApplied, "whole-file content matches submitted new_text after mechanical trailing-whitespace repair"
		}

		return plancontract.PhaseEvidenceStatusPending, "whole-file content does not match submitted new_text"
	}

	if strings.TrimSpace(edit.OldText) != "" {
		if strings.TrimSpace(edit.NewText) == "" {
			if !strings.Contains(current, edit.OldText) {
				return plancontract.PhaseEvidenceStatusApplied, "submitted old_text is absent after deletion"
			}

			return plancontract.PhaseEvidenceStatusPending, "submitted old_text is still present after deletion"
		}

		if strings.Contains(current, edit.NewText) {
			return plancontract.PhaseEvidenceStatusApplied, "submitted new_text is present"
		}

		if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.Contains(current, repaired) {
			return plancontract.PhaseEvidenceStatusApplied, "submitted new_text is present after mechanical trailing-whitespace repair"
		}

		if strings.Contains(current, edit.OldText) {
			return plancontract.PhaseEvidenceStatusPending, "submitted old_text is still present and new_text is absent"
		}

		return plancontract.PhaseEvidenceStatusUnknown, "neither submitted old_text nor new_text is present"
	}

	if edit.StartLine > 0 || edit.EndLine > 0 {
		if strings.TrimSpace(edit.Content) != "" && strings.Contains(current, edit.Content) {
			return plancontract.PhaseEvidenceStatusApplied, "submitted range content is present"
		}

		return plancontract.PhaseEvidenceStatusUnknown, "submitted range content is not present as an exact substring"
	}

	if strings.TrimSpace(edit.NewText) != "" && strings.Contains(current, edit.NewText) {
		return plancontract.PhaseEvidenceStatusApplied, "submitted new_text is present"
	}

	if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.TrimSpace(repaired) != "" && strings.Contains(current, repaired) {
		return plancontract.PhaseEvidenceStatusApplied, "submitted new_text is present after mechanical trailing-whitespace repair"
	}

	return plancontract.PhaseEvidenceStatusUnknown, "submitted edit shape has no deterministic postcondition"
}

func loadPlanExecutePacketEvidence(root string, packID string) PhaseEvidencePacket {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return PhaseEvidencePacket{Status: PhaseEvidencePacketStatusNotProvided}
	}

	if !isPlanExecutePackID(packID) {
		return PhaseEvidencePacket{PackID: packID, Status: PhaseEvidencePacketStatusInvalidPackID, Error: "pack_id must be a 16-character lowercase hex context view id"}
	}

	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, packID)
	if err != nil {
		return PhaseEvidencePacket{PackID: packID, Status: PhaseEvidencePacketStatusError, Error: err.Error()}
	}

	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		packet := PhaseEvidencePacketFromView(view)
		if !view.Fresh {
			packet.Status = PhaseEvidencePacketStatusAvailableStale
			packet.Fresh = false
			packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
		}

		return packet
	case comprehension.ViewLoadStateStale:
		packet := PhaseEvidencePacketFromView(view)

		packet.Status = PhaseEvidencePacketStatusAvailableStale
		packet.Fresh = false
		packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")

		return packet
	}

	return PhaseEvidencePacket{PackID: packID, Status: PhaseEvidencePacketStatusMiss}
}

func PhaseEvidencePacketFromView(view comprehension.PackResult) PhaseEvidencePacket {
	proj := comprehension.NewCompactPackProjection(view)
	packet := PhaseEvidencePacket{
		PackID:           strings.TrimSpace(view.PackID),
		Status:           PhaseEvidencePacketStatusAvailable,
		IndexID:          strings.TrimSpace(view.IndexID),
		Fresh:            view.Fresh,
		Confidence:       strings.TrimSpace(view.Confidence.Level),
		Quality:          packQualityFromComprehension(proj.Quality),
		StoreKind:        firstNonEmptyString(strings.TrimSpace(view.StoreKind), strings.TrimSpace(view.Provenance.StoreKind)),
		CandidateFiles:   PhaseEvidenceFiles(view.CandidateFiles, 8),
		CandidateSymbols: PhaseEvidenceSymbols(view.CandidateSymbols, 8),
	}

	packet.UnresolvedGaps = append(packet.UnresolvedGaps, cappedStrings(view.UnresolvedGaps, 8)...)

	for _, risk := range cappedStrings(view.Risks, 4) {
		packet.UnresolvedGaps = append(packet.UnresolvedGaps, "risk="+risk)
	}

	for _, test := range view.Tests {
		packet.SuggestedTests = append(packet.SuggestedTests, PhaseEvidenceTest{
			Command:  strings.TrimSpace(test.Command),
			Path:     strings.TrimSpace(test.Path),
			Symbol:   strings.TrimSpace(test.Symbol),
			Reason:   strings.TrimSpace(test.Reason),
			Required: test.Required,
		})
		if len(packet.SuggestedTests) >= 8 {
			break
		}
	}

	return packet
}

func PhaseEvidenceFiles(files []comprehension.PackFile, limit int) []PhaseEvidenceFile {
	out := make([]PhaseEvidenceFile, 0, phaseEvidenceMinInt(limit, len(files)))
	for _, file := range files {
		out = append(out, PhaseEvidenceFile{
			Path:       strings.TrimSpace(file.Path),
			Role:       strings.TrimSpace(file.Role),
			Reason:     strings.TrimSpace(file.Reason),
			Confidence: strings.TrimSpace(file.Confidence),
			Stale:      file.Stale,
			Hub:        file.Hub,
		})
		if len(out) >= limit {
			break
		}
	}

	return out
}

func PhaseEvidenceSymbols(symbols []comprehension.PackSymbol, limit int) []PhaseEvidenceSymbol {
	out := make([]PhaseEvidenceSymbol, 0, phaseEvidenceMinInt(limit, len(symbols)))
	for _, symbol := range symbols {
		out = append(out, PhaseEvidenceSymbol{
			Name:       firstNonEmptyString(strings.TrimSpace(symbol.QualifiedName), strings.TrimSpace(symbol.ID)),
			Kind:       strings.TrimSpace(symbol.Kind),
			Path:       strings.TrimSpace(symbol.Path),
			Reason:     strings.TrimSpace(symbol.Reason),
			Confidence: strings.TrimSpace(symbol.Confidence),
			Stale:      symbol.Stale,
		})
		if len(out) >= limit {
			break
		}
	}

	return out
}

func packetSuggestedTests(packet PhaseEvidencePacket) []PhaseEvidenceTest {
	return append([]PhaseEvidenceTest(nil), packet.SuggestedTests...)
}

func phaseEvidenceOpenObligations(plan planartifact.PlanArtifact, packet PhaseEvidencePacket, changedPaths []string, requiredPaths []string, submittedPlan []plancontract.PhaseEvidenceCheck, sourceSanity []plancontract.PhaseEvidenceCheck, verification []plancontract.PhaseEvidenceCheck, patchHygiene plancontract.PhaseEvidenceCheck, coversSourceSanity SourceSanityCoverage) []string {
	coversSourceSanity = normalizeDependencies(Dependencies{SourceSanityCovers: coversSourceSanity}).SourceSanityCovers
	var out []string

	if strings.TrimSpace(plan.PackID) != "" {
		if !phaseEvidencePacketAvailable(packet.Status) {
			out = append(out, "context_packet_unavailable:"+firstNonEmptyString(string(packet.Status), "unknown"))
		}
	}

	for i, check := range submittedPlan {
		status := firstNonEmptyString(strings.TrimSpace(string(check.Status)), "unknown")
		if status == "applied" {
			continue
		}

		out = append(out, fmt.Sprintf("submitted_edit_%s:%s#%d", status, firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"), i+1))
	}

	coveredRequiredPaths := map[string]struct{}{}
	for _, path := range mergePlanExecutePaths(changedPaths) {
		coveredRequiredPaths[path] = struct{}{}
	}
	sourceSatisfiedViolations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        requiredPaths,
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             planartifact.SourceSatisfiedEvidenceRefs(plan),
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	})
	for path := range plancontract.SourceSatisfiedCoveragePathSet(plan.Paths.SourceSatisfied, sourceSatisfiedViolations) {
		coveredRequiredPaths[path] = struct{}{}
	}
	for _, path := range mergePlanExecutePaths(requiredPaths) {
		if _, covered := coveredRequiredPaths[path]; !covered {
			out = append(out, "required_path_uncovered:"+path)
		}
	}

	if patchHygiene.Status == "failed" {
		out = append(out, "patch_hygiene_failed")
	} else if patchHygiene.Status == "unavailable" {
		out = append(out, "patch_hygiene_unavailable")
	}

	sourceSanityByPath := map[string]bool{}

	for _, check := range sourceSanity {
		if path := strings.TrimSpace(check.Path); path != "" {
			sourceSanityByPath[path] = true
		}
	}

	for _, path := range mergePlanExecutePaths(changedPaths) {
		if coversSourceSanity(path) {
			if !sourceSanityByPath[path] {
				out = append(out, "source_sanity_missing:"+path)
			}
		}
	}

	for _, check := range sourceSanity {
		status := firstNonEmptyString(strings.TrimSpace(string(check.Status)), "unknown")
		if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
			out = append(out, "source_sanity_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"))
		}
	}

	for _, check := range verification {
		status := firstNonEmptyString(strings.TrimSpace(string(check.Status)), "unknown")
		if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
			out = append(out, "verification_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Command), "(unknown)"))
		}
	}

	return cappedStrings(out, 16)
}

func phaseEvidencePacketAvailable(status PhaseEvidencePacketStatus) bool {
	return status == PhaseEvidencePacketStatusAvailable || status == PhaseEvidencePacketStatusAvailableStale
}

func phaseEvidencePacketSummaryStatus(status PhaseEvidencePacketStatus) plancontract.PhaseEvidenceSummaryStatus {
	switch status {
	case PhaseEvidencePacketStatusAvailable:
		return plancontract.PhaseEvidenceSummaryStatusPassed
	case PhaseEvidencePacketStatusAvailableStale:
		return plancontract.PhaseEvidenceSummaryStatusOpen
	case PhaseEvidencePacketStatusNotProvided:
		return plancontract.PhaseEvidenceSummaryStatusNotApplicable
	case PhaseEvidencePacketStatusInvalidPackID, PhaseEvidencePacketStatusError, PhaseEvidencePacketStatusMiss:
		return plancontract.PhaseEvidenceSummaryStatusFailed
	default:
		return plancontract.PhaseEvidenceSummaryStatusFailed
	}
}

func phaseEvidenceCheckSummaryStatus(check plancontract.PhaseEvidenceCheck) plancontract.PhaseEvidenceSummaryStatus {
	if strings.TrimSpace(check.Error) != "" {
		return plancontract.PhaseEvidenceSummaryStatusFailed
	}

	switch check.Status {
	case plancontract.PhaseEvidenceStatusApplied:
		return plancontract.PhaseEvidenceSummaryStatusApplied
	case plancontract.PhaseEvidenceStatusPending,
		plancontract.PhaseEvidenceStatusMissing,
		plancontract.PhaseEvidenceStatusSkipped,
		plancontract.PhaseEvidenceStatusUnknown:
		return plancontract.PhaseEvidenceSummaryStatusOpen
	}

	if plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
		return plancontract.PhaseEvidenceSummaryStatusPassed
	}

	return plancontract.PhaseEvidenceSummaryStatusFailed
}

func phaseEvidenceDecisionSummary(packet PhaseEvidencePacket, submittedPlan []plancontract.PhaseEvidenceCheck, sourceSanity []plancontract.PhaseEvidenceCheck, verification []plancontract.PhaseEvidenceCheck, patchHygiene plancontract.PhaseEvidenceCheck) []phaseEvidenceSummary {
	out := []phaseEvidenceSummary{
		{Kind: PhaseEvidenceSummaryKindContextPacket, Status: phaseEvidencePacketSummaryStatus(packet.Status), Summary: PhaseEvidencePacketSummary(packet)},
		{Kind: PhaseEvidenceSummaryKindPatchHygiene, Status: phaseEvidenceCheckSummaryStatus(patchHygiene), Summary: strings.TrimSpace(patchHygiene.Error)},
	}
	submittedStatus := plancontract.PhaseEvidenceSummaryStatusNotApplicable

	if len(submittedPlan) > 0 {
		submittedStatus = plancontract.PhaseEvidenceSummaryStatusApplied

		for _, check := range submittedPlan {
			if check.Status != plancontract.PhaseEvidenceStatusApplied {
				submittedStatus = plancontract.PhaseEvidenceSummaryStatusOpen
				break
			}
		}
	}

	out = append(out, phaseEvidenceSummary{Kind: PhaseEvidenceSummaryKindSubmittedPlan, Status: submittedStatus, Summary: fmt.Sprintf("%d edit(s)", len(submittedPlan))})

	status := plancontract.PhaseEvidenceSummaryStatusNotApplicable

	if len(sourceSanity) > 0 {
		status = plancontract.PhaseEvidenceSummaryStatusPassed

		for _, check := range sourceSanity {
			if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
				status = plancontract.PhaseEvidenceSummaryStatusFailed
				break
			}
		}
	}

	out = append(out, phaseEvidenceSummary{Kind: PhaseEvidenceSummaryKindSourceSanity, Status: status, Summary: fmt.Sprintf("%d check(s)", len(sourceSanity))})

	verificationStatus := plancontract.PhaseEvidenceSummaryStatusNotApplicable

	if len(verification) > 0 {
		verificationStatus = plancontract.PhaseEvidenceSummaryStatusPassed

		for _, check := range verification {
			if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
				verificationStatus = plancontract.PhaseEvidenceSummaryStatusFailed
				break
			}
		}
	}

	out = append(out, phaseEvidenceSummary{Kind: PhaseEvidenceSummaryKindVerification, Status: verificationStatus, Summary: fmt.Sprintf("%d command(s)", len(verification))})

	return out
}

func PhaseEvidencePacketSummary(packet PhaseEvidencePacket) string {
	switch packet.Status {
	case PhaseEvidencePacketStatusAvailable, PhaseEvidencePacketStatusAvailableStale:
		return fmt.Sprintf("%d file(s), %d symbol(s), %d suggested test(s), %d unresolved gap(s)", len(packet.CandidateFiles), len(packet.CandidateSymbols), len(packet.SuggestedTests), len(packet.UnresolvedGaps))
	case PhaseEvidencePacketStatusError:
		return strings.TrimSpace(packet.Error)
	default:
		return ""
	}
}

func renderPlanExecutePhaseEvidence(bundle PhaseEvidenceBundle) string {
	var b strings.Builder

	fmt.Fprintf(&b, "schema: %s\nphase: %s\n", bundle.SchemaVersion, bundle.Phase)
	fmt.Fprintf(&b, "changed_paths: %s\n", formatInlineList(bundle.ChangedPaths))
	fmt.Fprintf(&b, "required_paths: %s\n", formatInlineList(bundle.RequiredPaths))
	fmt.Fprintf(&b, "allowed_repair_paths: %s\n", formatInlineList(bundle.AllowedPaths))
	fmt.Fprintf(&b, "edit_set_obligations: %s\n\n", formatInlineList(bundle.EditSetObligations.Kinds()))
	fmt.Fprintf(&b, "context_packet: status=%s", bundle.Packet.Status)

	if bundle.Packet.PackID != "" {
		fmt.Fprintf(&b, " pack_id=%s", bundle.Packet.PackID)
	}

	if bundle.Packet.IndexID != "" {
		fmt.Fprintf(&b, " index_id=%s fresh=%t", bundle.Packet.IndexID, bundle.Packet.Fresh)
	}

	if summary := PhaseEvidencePacketSummary(bundle.Packet); summary != "" {
		fmt.Fprintf(&b, " summary=%s", summary)
	}

	b.WriteString("\n")

	if bundle.Packet.Quality.LocalizationConfidence != "" {
		fmt.Fprintf(&b, "packet_quality: localization_confidence=%s source_owner_found=%t\n",
			bundle.Packet.Quality.LocalizationConfidence,
			bundle.Packet.Quality.SourceOwnerFound)
	}

	fmt.Fprintf(&b, "executor_handoff_packet: state=%s reason=%s handoff_created=%t packet_present=%t packet_not_used_by_design=%t packet_freshness_state=%s",
		bundle.ExecutorHandoff.PacketState,
		bundle.ExecutorHandoff.Reason,
		bundle.ExecutorHandoff.HandoffCreated,
		bundle.ExecutorHandoff.PacketPresent,
		bundle.ExecutorHandoff.PacketNotUsed,
		bundle.ExecutorHandoff.PacketFreshness,
	)

	if bundle.ExecutorHandoff.PackID != "" {
		fmt.Fprintf(&b, " pack_id=%s", bundle.ExecutorHandoff.PackID)
	}

	if bundle.ExecutorHandoff.IndexID != "" {
		fmt.Fprintf(&b, " index_id=%s fresh=%t", bundle.ExecutorHandoff.IndexID, bundle.ExecutorHandoff.Fresh)
	}

	b.WriteString("\n")

	if bundle.ExecutorHandoff.Quality.LocalizationConfidence != "" {
		fmt.Fprintf(&b, "executor_handoff_quality: localization_confidence=%s source_owner_found=%t\n",
			bundle.ExecutorHandoff.Quality.LocalizationConfidence,
			bundle.ExecutorHandoff.Quality.SourceOwnerFound)
	}

	if len(bundle.Packet.CandidateFiles) > 0 {
		b.WriteString("candidate_files:\n")

		for _, file := range bundle.Packet.CandidateFiles {
			fmt.Fprintf(&b, "- `%s` role=%s reason=%s confidence=%s\n", file.Path, file.Role, file.Reason, file.Confidence)
		}
	}

	if len(bundle.Packet.CandidateSymbols) > 0 {
		b.WriteString("candidate_symbols:\n")

		for _, symbol := range bundle.Packet.CandidateSymbols {
			fmt.Fprintf(&b, "- `%s` (%s) path=%s reason=%s\n", symbol.Name, symbol.Kind, symbol.Path, symbol.Reason)
		}
	}

	if len(bundle.SuggestedTests) > 0 {
		b.WriteString("suggested_tests:\n")

		for _, test := range bundle.SuggestedTests {
			fmt.Fprintf(&b, "- `%s` path=%s reason=%s\n", test.Command, test.Path, test.Reason)
		}
	}

	if len(bundle.SubmittedPlan) > 0 {
		b.WriteString("submitted_plan_state:\n")

		for _, check := range bundle.SubmittedPlan {
			fmt.Fprintf(&b, "- `%s` status=%s summary=%s error=%s\n", check.Path, check.Status, check.Summary, check.Error)
		}
	}

	if len(bundle.SourceSanity) > 0 {
		b.WriteString("source_sanity:\n")

		for _, check := range bundle.SourceSanity {
			fmt.Fprintf(&b, "- `%s` status=%s summary=%s error=%s\n", check.Path, check.Status, check.Summary, check.Error)
		}
	}

	if len(bundle.Verification) > 0 {
		b.WriteString("verification:\n")

		for _, check := range bundle.Verification {
			workingDir := ""
			if strings.TrimSpace(check.WorkingDir) != "" {
				workingDir = " working_dir=" + strings.TrimSpace(check.WorkingDir)
			}

			fmt.Fprintf(&b, "- `%s`%s status=%s summary=%s error=%s\n", check.Command, workingDir, check.Status, check.Summary, check.Error)
		}
	}

	fmt.Fprintf(&b, "patch_hygiene: status=%s", bundle.PatchHygiene.Status)

	if strings.TrimSpace(bundle.PatchHygiene.Error) != "" {
		fmt.Fprintf(&b, " error=%s", strings.TrimSpace(bundle.PatchHygiene.Error))
	}

	b.WriteString("\n")

	if len(bundle.UnresolvedGaps) > 0 {
		b.WriteString("unresolved_gaps:\n")

		for _, gap := range bundle.UnresolvedGaps {
			fmt.Fprintf(&b, "- %s\n", gap)
		}
	}

	if len(bundle.OpenObligations) > 0 {
		b.WriteString("open_obligations:\n")

		for _, obligation := range bundle.OpenObligations {
			fmt.Fprintf(&b, "- %s\n", obligation)
		}
	}

	return b.String()
}

func phaseEvidenceTelemetry(bundle PhaseEvidenceBundle) map[string]any {
	return map[string]any{
		"schema_version":            bundle.SchemaVersion,
		"phase":                     bundle.Phase,
		"changed_paths":             cappedStrings(bundle.ChangedPaths, 16),
		"required_paths":            cappedStrings(bundle.RequiredPaths, 16),
		"allowed_paths":             cappedStrings(bundle.AllowedPaths, 16),
		"edit_set_obligations":      bundle.EditSetObligations.Kinds(),
		"edit_set_obligation_count": len(bundle.EditSetObligations.Normalized()),
		"packet_status":             bundle.Packet.Status,
		"pack_id":                   bundle.Packet.PackID,
		"executor_packet_state":     bundle.ExecutorHandoff.PacketState,
		"executor_packet_reason":    bundle.ExecutorHandoff.Reason,
		"executor_handoff_id":       bundle.ExecutorHandoff.HandoffID,
		"handoff_created":           bundle.ExecutorHandoff.HandoffCreated,
		"packet_present":            bundle.ExecutorHandoff.PacketPresent,
		"packet_not_used_by_design": bundle.ExecutorHandoff.PacketNotUsed,
		"packet_freshness_state":    bundle.ExecutorHandoff.PacketFreshness,
		"candidate_files":           len(bundle.Packet.CandidateFiles),
		"candidate_file_paths":      phaseEvidenceTelemetryFilePaths(bundle.Packet.CandidateFiles, 8),
		"candidate_symbols":         len(bundle.Packet.CandidateSymbols),
		"candidate_symbol_names": phaseEvidenceTelemetrySymbolNames(
			bundle.Packet.CandidateSymbols,
			8,
		),
		"suggested_tests": len(bundle.SuggestedTests),
		"suggested_test_commands": phaseEvidenceTelemetryTestCommands(
			bundle.SuggestedTests,
			8,
		),
		"submitted_plan_checks": len(bundle.SubmittedPlan),
		"submitted_plan":        PhaseEvidenceTelemetryChecks(bundle.SubmittedPlan, 8),
		"source_sanity_checks":  len(bundle.SourceSanity),
		"source_sanity":         PhaseEvidenceTelemetryChecks(bundle.SourceSanity, 8),
		"verification_checks":   len(bundle.Verification),
		"verification":          PhaseEvidenceTelemetryChecks(bundle.Verification, 8),
		"patch_hygiene_status":  bundle.PatchHygiene.Status,
		"unresolved_gap_count":  len(bundle.UnresolvedGaps),
		"unresolved_gaps":       cappedStrings(bundle.UnresolvedGaps, 8),
		"open_obligation_count": len(bundle.OpenObligations),
		"open_obligations":      bundle.OpenObligations,
	}
}

func phaseEvidenceTelemetryFilePaths(files []PhaseEvidenceFile, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(files)))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}

		out = append(out, path)
		if len(out) >= limit {
			break
		}
	}

	return out
}

func phaseEvidenceTelemetrySymbolNames(symbols []PhaseEvidenceSymbol, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(symbols)))
	for _, symbol := range symbols {
		name := firstNonEmptyString(strings.TrimSpace(symbol.Name), strings.TrimSpace(symbol.Path))
		if name == "" {
			continue
		}

		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}

	return out
}

func phaseEvidenceTelemetryTestCommands(tests []PhaseEvidenceTest, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(tests)))
	for _, test := range tests {
		command := firstNonEmptyString(strings.TrimSpace(test.Command), strings.TrimSpace(test.Path))
		if command == "" {
			continue
		}

		out = append(out, command)
		if len(out) >= limit {
			break
		}
	}

	return out
}

func PhaseEvidenceTelemetryChecks(checks []plancontract.PhaseEvidenceCheck, limit int) []map[string]string {
	out := make([]map[string]string, 0, phaseEvidenceMinInt(limit, len(checks)))
	for _, check := range checks {
		entry := map[string]string{
			"kind":   strings.TrimSpace(string(check.Kind)),
			"path":   strings.TrimSpace(check.Path),
			"status": strings.TrimSpace(string(check.Status)),
		}
		if command := strings.TrimSpace(check.Command); command != "" {
			entry["command"] = phaseEvidenceTruncate(command, 240)
		}

		if workingDir := strings.TrimSpace(check.WorkingDir); workingDir != "" {
			entry["working_dir"] = phaseEvidenceTruncate(workingDir, 240)
		}

		if summary := strings.TrimSpace(check.Summary); summary != "" {
			entry["summary"] = phaseEvidenceTruncate(summary, 240)
		}

		if errText := strings.TrimSpace(check.Error); errText != "" {
			entry["error"] = phaseEvidenceTruncate(errText, 240)
		}

		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}

	return out
}

func phaseEvidenceTruncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}

	for i := range text {
		if i >= limit {
			return text[:i] + "...(truncated)"
		}
	}

	return text
}

func cappedStrings(values []string, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(values)))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}

	return out
}

func phaseEvidenceMinInt(a int, b int) int {
	if a < b {
		return a
	}

	return b
}

func formatInlineList(values []string) string {
	values = cappedStrings(values, 12)
	if len(values) == 0 {
		return "(none)"
	}

	return strings.Join(values, ", ")
}
