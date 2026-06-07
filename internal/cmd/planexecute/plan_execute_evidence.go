package planexecute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/comprehension"
	"idiom/internal/planexecute"
	"idiom/internal/workspace"
)

const phaseEvidenceSchemaVersion = "idiom.phase_evidence.v1"

type phaseEvidenceBundle struct {
	SchemaVersion      string                         `json:"schema_version"`
	Phase              string                         `json:"phase"`
	GeneratedAt        string                         `json:"generated_at"`
	ChangedPaths       []string                       `json:"changed_paths,omitempty"`
	RequiredPaths      []string                       `json:"required_paths,omitempty"`
	AllowedPaths       []string                       `json:"allowed_paths,omitempty"`
	EditSetObligations planexecute.EditSetObligations `json:"edit_set_obligations,omitempty"`
	Packet             phaseEvidencePacket            `json:"packet"`
	ExecutorHandoff    phaseEvidenceHandoff           `json:"executor_handoff"`
	SubmittedPlan      []phaseEvidenceCheck           `json:"submitted_plan_state,omitempty"`
	SourceSanity       []phaseEvidenceCheck           `json:"source_sanity,omitempty"`
	Verification       []phaseEvidenceCheck           `json:"verification,omitempty"`
	PatchHygiene       phaseEvidenceCheck             `json:"patch_hygiene"`
	SuggestedTests     []phaseEvidenceTest            `json:"suggested_tests,omitempty"`
	UnresolvedGaps     []string                       `json:"unresolved_gaps,omitempty"`
	OpenObligations    []string                       `json:"open_obligations,omitempty"`
	DecisionEvidence   []phaseEvidenceSummary         `json:"decision_evidence,omitempty"`
}

type phaseEvidencePacket struct {
	PackID           string                `json:"pack_id,omitempty"`
	Status           string                `json:"status"`
	Error            string                `json:"error,omitempty"`
	IndexID          string                `json:"index_id,omitempty"`
	Fresh            bool                  `json:"fresh,omitempty"`
	Confidence       string                `json:"confidence,omitempty"`
	StoreKind        string                `json:"store_kind,omitempty"`
	CandidateFiles   []phaseEvidenceFile   `json:"candidate_files,omitempty"`
	CandidateSymbols []phaseEvidenceSymbol `json:"candidate_symbols,omitempty"`
	SuggestedTests   []phaseEvidenceTest   `json:"suggested_tests,omitempty"`
	UnresolvedGaps   []string              `json:"unresolved_gaps,omitempty"`
}

type phaseEvidenceHandoff struct {
	PacketState      string `json:"packet_state"`
	Reason           string `json:"reason,omitempty"`
	PlanID           string `json:"plan_id,omitempty"`
	HandoffID        string `json:"handoff_id,omitempty"`
	HandoffStatus    string `json:"handoff_status,omitempty"`
	PackID           string `json:"pack_id,omitempty"`
	DeclaredPackID   string `json:"declared_pack_id,omitempty"`
	IndexID          string `json:"index_id,omitempty"`
	Fresh            bool   `json:"fresh,omitempty"`
	CandidateFiles   int    `json:"candidate_files,omitempty"`
	CandidateSymbols int    `json:"candidate_symbols,omitempty"`
	Tests            int    `json:"tests,omitempty"`
}

type phaseEvidenceFile struct {
	Path       string `json:"path"`
	Role       string `json:"role,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	Hub        bool   `json:"hub,omitempty"`
}

type phaseEvidenceSymbol struct {
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Path       string `json:"path,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
}

type phaseEvidenceTest struct {
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type phaseEvidenceCheck = planexecute.PhaseEvidenceCheck

type phaseEvidenceSummary struct {
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

func collectPlanExecutePhaseEvidence(root string, phase string, plan workspace.PlanArtifact, changedPaths []string, requiredPaths []string, allowedPaths []string, reviewEvidence reviewRepairEvidence, executorReport executorPhaseReport) phaseEvidenceBundle {
	handoff := executorReport.Handoff
	if strings.TrimSpace(handoff.State) == "" {
		handoff = classifyExecutorHandoffPacket(root, plan)
	}
	packet := loadPlanExecutePacketEvidence(root, handoff.PackID)
	submittedPlan := collectSubmittedPlanState(root, plan)
	sourceSanity := append([]phaseEvidenceCheck(nil), executorReport.SourceSanity...)
	patchHygiene := phaseEvidenceCheck{
		Kind:   "git_diff_check",
		Status: "passed",
	}
	if reviewEvidence.DiffCheckExit != 0 {
		patchHygiene.Status = "failed"
		if reviewEvidence.DiffCheckExit != 1 {
			patchHygiene.Status = "unavailable"
		}
		patchHygiene.Error = strings.TrimSpace(reviewEvidence.DiffCheck)
	}
	bundle := phaseEvidenceBundle{
		SchemaVersion:    phaseEvidenceSchemaVersion,
		Phase:            firstNonEmptyString(strings.TrimSpace(phase), "unknown"),
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		ChangedPaths:     mergePlanExecutePaths(changedPaths),
		RequiredPaths:    mergePlanExecutePaths(requiredPaths),
		AllowedPaths:     mergePlanExecutePaths(allowedPaths),
		Packet:           packet,
		ExecutorHandoff:  phaseEvidenceHandoffFromExecutor(handoff),
		SubmittedPlan:    submittedPlan,
		SourceSanity:     sourceSanity,
		Verification:     append([]phaseEvidenceCheck(nil), executorReport.Verification...),
		PatchHygiene:     patchHygiene,
		SuggestedTests:   packetSuggestedTests(packet),
		UnresolvedGaps:   append([]string(nil), packet.UnresolvedGaps...),
		OpenObligations:  phaseEvidenceOpenObligations(plan, packet, changedPaths, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene),
		DecisionEvidence: phaseEvidenceDecisionSummary(packet, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene),
	}
	return bundle
}

func phaseEvidenceHandoffFromExecutor(handoff executorHandoffPacket) phaseEvidenceHandoff {
	return phaseEvidenceHandoff{
		PacketState:      firstNonEmptyString(strings.TrimSpace(handoff.State), executorHandoffStateMissing),
		Reason:           strings.TrimSpace(handoff.Reason),
		PlanID:           strings.TrimSpace(handoff.PlanID),
		HandoffID:        strings.TrimSpace(handoff.HandoffID),
		HandoffStatus:    strings.TrimSpace(handoff.HandoffStatus),
		PackID:           strings.TrimSpace(handoff.PackID),
		DeclaredPackID:   strings.TrimSpace(handoff.DeclaredPackID),
		IndexID:          strings.TrimSpace(handoff.IndexID),
		Fresh:            handoff.Fresh,
		CandidateFiles:   handoff.CandidateFiles,
		CandidateSymbols: handoff.CandidateSymbols,
		Tests:            handoff.Tests,
	}
}

func collectSubmittedPlanState(root string, plan workspace.PlanArtifact) []phaseEvidenceCheck {
	out := make([]phaseEvidenceCheck, 0, len(plan.Edits))
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		check := phaseEvidenceCheck{
			Kind:   "submitted_edit",
			Path:   path,
			Status: "unknown",
		}
		if path == "" {
			check.Status = "failed"
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
			check.Status = "failed"
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

func submittedPlanEditStatus(current string, edit workspace.PlanEdit) (string, string) {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		if current == edit.NewText {
			return "applied", "whole-file content matches submitted new_text"
		}
		if repaired := planexecute.TrimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && current == repaired {
			return "applied", "whole-file content matches submitted new_text after mechanical trailing-whitespace repair"
		}
		return "pending", "whole-file content does not match submitted new_text"
	}
	if strings.TrimSpace(edit.OldText) != "" {
		if strings.TrimSpace(edit.NewText) == "" {
			if !strings.Contains(current, edit.OldText) {
				return "applied", "submitted old_text is absent after deletion"
			}
			return "pending", "submitted old_text is still present after deletion"
		}
		if strings.Contains(current, edit.NewText) {
			return "applied", "submitted new_text is present"
		}
		if repaired := planexecute.TrimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.Contains(current, repaired) {
			return "applied", "submitted new_text is present after mechanical trailing-whitespace repair"
		}
		if strings.Contains(current, edit.OldText) {
			return "pending", "submitted old_text is still present and new_text is absent"
		}
		return "unknown", "neither submitted old_text nor new_text is present"
	}
	if edit.StartLine > 0 || edit.EndLine > 0 {
		if strings.TrimSpace(edit.Content) != "" && strings.Contains(current, edit.Content) {
			return "applied", "submitted range content is present"
		}
		return "unknown", "submitted range content is not present as an exact substring"
	}
	if strings.TrimSpace(edit.NewText) != "" && strings.Contains(current, edit.NewText) {
		return "applied", "submitted new_text is present"
	}
	if repaired := planexecute.TrimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.TrimSpace(repaired) != "" && strings.Contains(current, repaired) {
		return "applied", "submitted new_text is present after mechanical trailing-whitespace repair"
	}
	return "unknown", "submitted edit shape has no deterministic postcondition"
}

func loadPlanExecutePacketEvidence(root string, packID string) phaseEvidencePacket {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return phaseEvidencePacket{Status: "not_provided"}
	}
	if !isPlanExecutePackID(packID) {
		return phaseEvidencePacket{PackID: packID, Status: "invalid_pack_id", Error: "pack_id must be a 16-character lowercase hex context view id"}
	}
	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, packID)
	if err != nil {
		return phaseEvidencePacket{PackID: packID, Status: "error", Error: err.Error()}
	}
	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		packet := phaseEvidencePacketFromView(view)
		if !view.Fresh {
			packet.Status = "available_stale"
			packet.Fresh = false
			packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
		}
		return packet
	case comprehension.ViewLoadStateStale:
		packet := phaseEvidencePacketFromView(view)
		packet.Status = "available_stale"
		packet.Fresh = false
		packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
		return packet
	}
	view, artifactHit, artifactErr := loadPlanExecutePacketArtifact(root, packID)
	if artifactErr != nil {
		return phaseEvidencePacket{PackID: packID, Status: "error", Error: artifactErr.Error()}
	}
	if !artifactHit {
		return phaseEvidencePacket{PackID: packID, Status: "miss"}
	}
	packet := phaseEvidencePacketFromView(view)
	packet.Status = "available_stale"
	packet.Fresh = false
	packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
	return packet
}

func loadPlanExecutePacketArtifact(root string, packID string) (comprehension.PackResult, bool, error) {
	path := filepath.Join(strings.TrimSpace(root), ".idiom", "comprehension", "views", packID+".json")
	body, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return comprehension.PackResult{}, false, nil
	}
	if err != nil {
		return comprehension.PackResult{}, false, err
	}
	var view comprehension.PackResult
	if err := json.Unmarshal(body, &view); err != nil {
		return comprehension.PackResult{}, false, err
	}
	if strings.TrimSpace(view.PackID) == "" {
		view.PackID = packID
	}
	return view, true, nil
}

func phaseEvidencePacketFromView(view comprehension.PackResult) phaseEvidencePacket {
	packet := phaseEvidencePacket{
		PackID:           strings.TrimSpace(view.PackID),
		Status:           "available",
		IndexID:          strings.TrimSpace(view.IndexID),
		Fresh:            view.Fresh,
		Confidence:       strings.TrimSpace(view.Confidence.Level),
		StoreKind:        firstNonEmptyString(strings.TrimSpace(view.StoreKind), strings.TrimSpace(view.Provenance.StoreKind)),
		CandidateFiles:   phaseEvidenceFiles(view.CandidateFiles, 8),
		CandidateSymbols: phaseEvidenceSymbols(view.CandidateSymbols, 8),
	}
	packet.UnresolvedGaps = append(packet.UnresolvedGaps, cappedStrings(view.UnresolvedGaps, 8)...)
	for _, risk := range cappedStrings(view.Risks, 4) {
		packet.UnresolvedGaps = append(packet.UnresolvedGaps, "risk="+risk)
	}
	for _, test := range view.Tests {
		packet.SuggestedTests = append(packet.SuggestedTests, phaseEvidenceTest{
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

func phaseEvidenceFiles(files []comprehension.PackFile, limit int) []phaseEvidenceFile {
	out := make([]phaseEvidenceFile, 0, phaseEvidenceMinInt(limit, len(files)))
	for _, file := range files {
		out = append(out, phaseEvidenceFile{
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

func phaseEvidenceSymbols(symbols []comprehension.PackSymbol, limit int) []phaseEvidenceSymbol {
	out := make([]phaseEvidenceSymbol, 0, phaseEvidenceMinInt(limit, len(symbols)))
	for _, symbol := range symbols {
		out = append(out, phaseEvidenceSymbol{
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

func packetSuggestedTests(packet phaseEvidencePacket) []phaseEvidenceTest {
	return append([]phaseEvidenceTest(nil), packet.SuggestedTests...)
}

func phaseEvidenceOpenObligations(plan workspace.PlanArtifact, packet phaseEvidencePacket, changedPaths []string, submittedPlan []phaseEvidenceCheck, sourceSanity []phaseEvidenceCheck, verification []phaseEvidenceCheck, patchHygiene phaseEvidenceCheck) []string {
	var out []string
	if strings.TrimSpace(plan.PackID) != "" {
		if packet.Status != "available" && packet.Status != "available_stale" {
			out = append(out, "context_packet_unavailable:"+firstNonEmptyString(packet.Status, "unknown"))
		}
	}
	for i, check := range submittedPlan {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status == "applied" {
			continue
		}
		out = append(out, fmt.Sprintf("submitted_edit_%s:%s#%d", status, firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"), i+1))
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
		if workspace.SourceSanityCoversPath(path) {
			if !sourceSanityByPath[path] {
				out = append(out, "source_sanity_missing:"+path)
			}
		}
	}
	for _, check := range sourceSanity {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status != "ok" && status != "passed" {
			out = append(out, "source_sanity_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"))
		}
	}
	for _, check := range verification {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status != "ok" && status != "passed" {
			out = append(out, "verification_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Command), "(unknown)"))
		}
	}
	return cappedStrings(out, 16)
}

func phaseEvidenceDecisionSummary(packet phaseEvidencePacket, submittedPlan []phaseEvidenceCheck, sourceSanity []phaseEvidenceCheck, verification []phaseEvidenceCheck, patchHygiene phaseEvidenceCheck) []phaseEvidenceSummary {
	out := []phaseEvidenceSummary{
		{Kind: "context_packet", Status: packet.Status, Summary: phaseEvidencePacketSummary(packet)},
		{Kind: "patch_hygiene", Status: patchHygiene.Status, Summary: strings.TrimSpace(patchHygiene.Error)},
	}
	submittedStatus := "not_applicable"
	if len(submittedPlan) > 0 {
		submittedStatus = "applied"
		for _, check := range submittedPlan {
			if check.Status != "applied" {
				submittedStatus = "open"
				break
			}
		}
	}
	out = append(out, phaseEvidenceSummary{Kind: "submitted_plan", Status: submittedStatus, Summary: fmt.Sprintf("%d edit(s)", len(submittedPlan))})
	status := "not_applicable"
	if len(sourceSanity) > 0 {
		status = "passed"
		for _, check := range sourceSanity {
			if check.Status != "ok" && check.Status != "passed" {
				status = "failed"
				break
			}
		}
	}
	out = append(out, phaseEvidenceSummary{Kind: "source_sanity", Status: status, Summary: fmt.Sprintf("%d check(s)", len(sourceSanity))})
	verificationStatus := "not_applicable"
	if len(verification) > 0 {
		verificationStatus = "passed"
		for _, check := range verification {
			if check.Status != "ok" && check.Status != "passed" {
				verificationStatus = "failed"
				break
			}
		}
	}
	out = append(out, phaseEvidenceSummary{Kind: "verification", Status: verificationStatus, Summary: fmt.Sprintf("%d command(s)", len(verification))})
	return out
}

func phaseEvidencePacketSummary(packet phaseEvidencePacket) string {
	switch packet.Status {
	case "available", "available_stale":
		return fmt.Sprintf("%d file(s), %d symbol(s), %d suggested test(s), %d unresolved gap(s)", len(packet.CandidateFiles), len(packet.CandidateSymbols), len(packet.SuggestedTests), len(packet.UnresolvedGaps))
	case "error":
		return strings.TrimSpace(packet.Error)
	default:
		return ""
	}
}

func phaseEvidenceTelemetry(bundle phaseEvidenceBundle) map[string]any {
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
		"submitted_plan":        phaseEvidenceTelemetryChecks(bundle.SubmittedPlan, 8),
		"source_sanity_checks":  len(bundle.SourceSanity),
		"source_sanity":         phaseEvidenceTelemetryChecks(bundle.SourceSanity, 8),
		"verification_checks":   len(bundle.Verification),
		"verification":          phaseEvidenceTelemetryChecks(bundle.Verification, 8),
		"patch_hygiene_status":  bundle.PatchHygiene.Status,
		"unresolved_gap_count":  len(bundle.UnresolvedGaps),
		"unresolved_gaps":       cappedStrings(bundle.UnresolvedGaps, 8),
		"open_obligation_count": len(bundle.OpenObligations),
		"open_obligations":      bundle.OpenObligations,
	}
}

func phaseEvidenceTelemetryFilePaths(files []phaseEvidenceFile, limit int) []string {
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

func phaseEvidenceTelemetrySymbolNames(symbols []phaseEvidenceSymbol, limit int) []string {
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

func phaseEvidenceTelemetryTestCommands(tests []phaseEvidenceTest, limit int) []string {
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

func phaseEvidenceTelemetryChecks(checks []phaseEvidenceCheck, limit int) []map[string]string {
	return planexecute.PhaseEvidenceTelemetryChecks(checks, limit)
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
