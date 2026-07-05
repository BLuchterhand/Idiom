package planexecute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/sessionid"
)

type PlanExecuteTerminalCompletionInput struct {
	ProgressPath                 string
	ExitCode                     int
	ProtocolHash                 string
	SourceSatisfied              bool
	SourceSatisfiedEvidencePaths []string
	SourceSatisfiedEvidence      []plancontract.SourceSatisfiedEvidenceRef
	RequiredAcceptanceEvidence   []plancontract.AcceptanceEvidenceRequired
	AcceptanceEvidenceIDs        []string
	AcceptanceEvidenceClaims     []plancontract.AcceptanceEvidenceClaim
	FailureID                    string
	RecordPath                   string
	PacketPath                   string
	NextAction                   string
	CompletedAt                  time.Time
}

func NewPlanExecuteTerminalCompletionRecord(state ProgressState, input PlanExecuteTerminalCompletionInput) plancontract.PlanExecuteTerminalCompletionRecord {
	acceptanceEvidenceIDs := input.AcceptanceEvidenceIDs
	if len(acceptanceEvidenceIDs) == 0 {
		acceptanceEvidenceIDs = state.AcceptanceEvidenceIDs
	}

	record := plancontract.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:                 plancontract.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                          plancontract.PlanExecuteTerminalCompletionRecordType,
		ProgressPath:                  input.ProgressPath,
		SessionID:                     state.SessionID,
		Phase:                         state.Phase,
		Status:                        plancontract.PlanExecuteTerminalStatus(state.Status),
		ReviewStatus:                  state.Review.Status,
		ExitCode:                      input.ExitCode,
		ReviewRepairSkipped:           state.Review.Status == plancontract.PlanExecuteReviewStatusSkipped,
		FailureKind:                   plancontract.PlanExecuteFailureKind(state.FailureKind),
		FailureReason:                 state.FailureReason,
		FailureID:                     input.FailureID,
		RecordPath:                    input.RecordPath,
		PacketPath:                    input.PacketPath,
		NextAction:                    recoverycontract.NextAction(strings.TrimSpace(input.NextAction)),
		ProtocolHash:                  firstPlanExecuteTerminalString(input.ProtocolHash, state.ScopeContractHash),
		RequiredEditPaths:             append([]string(nil), state.RequiredEditPaths...),
		AllowedEditPaths:              append([]string(nil), state.AllowedEditPaths...),
		WriteAllowedPaths:             append([]string(nil), state.WriteAllowedPaths...),
		ScopeContractID:               state.ScopeContractID,
		ScopeContractHash:             state.ScopeContractHash,
		SourceSatisfiedEvidencePaths:  append([]string(nil), input.SourceSatisfiedEvidencePaths...),
		SourceSatisfiedEvidence:       plancontract.NormalizeSourceSatisfiedEvidenceRefs(input.SourceSatisfiedEvidence),
		ChangedPaths:                  append([]string(nil), state.ChangedPaths...),
		SourceSanity:                  append([]plancontract.PhaseEvidenceCheck(nil), state.Executor.SourceSanity...),
		VerificationCommands:          append([]plancontract.VerificationCommand(nil), state.Executor.VerificationCommands...),
		Verification:                  append([]plancontract.PhaseEvidenceCheck(nil), state.Executor.Verification...),
		VerificationSkips:             append([]plancontract.VerificationSkip(nil), state.Executor.VerificationSkips...),
		VerificationRejects:           append([]plancontract.VerificationReject(nil), state.Executor.VerificationRejects...),
		RequiredAcceptanceEvidence:    append([]plancontract.AcceptanceEvidenceRequired(nil), input.RequiredAcceptanceEvidence...),
		RequiredAcceptanceEvidenceIDs: append([]string(nil), state.RequiredAcceptanceEvidenceIDs...),
		AcceptanceEvidenceIDs:         append([]string(nil), acceptanceEvidenceIDs...),
		AcceptanceEvidenceClaims:      append([]plancontract.AcceptanceEvidenceClaim(nil), input.AcceptanceEvidenceClaims...),
	}
	if !input.CompletedAt.IsZero() {
		record.TS = input.CompletedAt.UTC().Format(time.RFC3339Nano)
	}

	if input.SourceSatisfied {
		record.Status = plancontract.PlanExecuteTerminalStatusSourceSatisfied
		record.Outcome = plancontract.PlanExecuteTerminalOutcomeSourceSatisfied
		record.TerminalClass = plancontract.PlanExecuteTerminalClassSuccess
	}

	return plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
}

func planExecuteHarnessObservedSourceEvidence(paths []string) []plancontract.SourceSatisfiedEvidenceRef {
	out := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(paths))
	for _, path := range plancontract.UniqueScopePaths(paths) {
		out = append(out, plancontract.SourceSatisfiedEvidenceRef{Path: path, Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource})
	}

	return out
}

func ProgressStateHasTerminalCompletion(state ProgressState) bool {
	if state.ReviewResumeReady() {
		return false
	}

	status := canonicalPlanExecuteTerminalToken(string(state.Status))
	phase := canonicalPlanExecuteTerminalToken(string(state.Phase))
	reviewStatus := canonicalPlanExecuteTerminalToken(string(state.Review.Status))

	if phase == string(plancontract.PlanExecuteProgressPhaseComplete) {
		return true
	}

	switch status {
	case string(plancontract.PlanExecuteProgressStatusCompleted), string(plancontract.PlanExecuteProgressStatusFailed), string(plancontract.PlanExecuteProgressStatusDegraded), string(plancontract.PlanExecuteProgressStatusBlocked), string(plancontract.PlanExecuteProgressStatusSkipped):
		return true
	}

	switch reviewStatus {
	case string(plancontract.PlanExecuteProgressStatusCompleted), string(plancontract.PlanExecuteProgressStatusFailed), string(plancontract.PlanExecuteProgressStatusDegraded), string(plancontract.PlanExecuteProgressStatusBlocked), string(plancontract.PlanExecuteProgressStatusSkipped):
		return true
	default:
		return false
	}
}

func PlanExecuteTerminalCompletionRecordFromProgressState(state ProgressState, input PlanExecuteTerminalCompletionInput) (plancontract.PlanExecuteTerminalCompletionRecord, bool) {
	if !ProgressStateHasTerminalCompletion(state) {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false
	}

	return NewPlanExecuteTerminalCompletionRecord(state, input), true
}

func canonicalPlanExecuteTerminalToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")

	switch value {
	case "complete", "success", "successful", "succeeded":
		return string(plancontract.PlanExecuteProgressStatusCompleted)
	default:
		return value
	}
}

func normalizePlanExecuteTerminalStableIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}

	for _, value := range values {
		value = plancontract.NormalizeStableContractIDPart(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	sort.Strings(out)

	return out
}

func firstPlanExecuteTerminalString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}

	return fallback
}

func SavePlanExecuteTerminalCompletionRecord(root string, record plancontract.PlanExecuteTerminalCompletionRecord) (string, error) {
	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
	if strings.TrimSpace(record.SessionID) == "" {
		return "", fmt.Errorf("plan-execute terminal completion missing session id")
	}
	if err := plancontract.ValidatePlanExecuteTerminalCompletionRecord(record); err != nil {
		return "", err
	}

	fullPath, relPath, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir plan-execute terminal completion dir: %w", err)
	}

	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plan-execute terminal completion: %w", err)
	}

	if err := atomicfile.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		return relPath, fmt.Errorf("write plan-execute terminal completion: %w", err)
	}

	return relPath, nil
}

func TryLoadPlanExecuteTerminalCompletionRecord(root string, sessionID string) (plancontract.PlanExecuteTerminalCompletionRecord, string, bool, error) {
	fullPath, relPath, err := planExecuteTerminalCompletionPath(root, sessionID)
	if err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, "", false, err
	}

	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, nil
	}

	if err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("read plan-execute terminal completion %s: %w", relPath, err)
	}

	var record plancontract.PlanExecuteTerminalCompletionRecord

	if err := json.Unmarshal(body, &record); err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("decode plan-execute terminal completion %s: %w", relPath, err)
	}

	if record.SchemaVersion != "" && record.SchemaVersion != plancontract.PlanExecuteTerminalCompletionSchemaVersion {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("unsupported plan-execute terminal completion schema %q in %s", record.SchemaVersion, relPath)
	}

	if strings.TrimSpace(record.SessionID) == "" {
		record.SessionID = strings.TrimSpace(sessionID)
	} else if strings.TrimSpace(record.SessionID) != strings.TrimSpace(sessionID) {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("plan-execute terminal completion session id %q does not match requested session %q in %s", record.SessionID, sessionID, relPath)
	}

	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
	if err := plancontract.ValidatePlanExecuteTerminalCompletionRecord(record); err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, relPath, false, fmt.Errorf("invalid plan-execute terminal completion %s: %w", relPath, err)
	}

	return record, relPath, true, nil
}

func planExecuteTerminalCompletionPath(root string, sessionID string) (string, string, error) {
	root = strings.TrimSpace(root)
	sessionID = strings.TrimSpace(sessionID)

	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}

	if !sessionid.Valid(sessionID) {
		return "", "", fmt.Errorf("invalid plan-execute terminal completion session id %q", sessionID)
	}

	relPath := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", sessionID+".terminal.json"))

	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}
