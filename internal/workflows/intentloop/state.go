package intentloop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/atomicfile"
)

const StateSchemaVersion = "idiom.intent_loop.state.v1"

var validIntentLoopSessionID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type IntentLoopState struct {
	SchemaVersion     string                    `json:"schema_version"`
	SessionID         string                    `json:"session_id"`
	OriginalPrompt    string                    `json:"original_prompt"`
	PromptSHA256      string                    `json:"prompt_sha256"`
	CreatedAt         string                    `json:"created_at"`
	UpdatedAt         string                    `json:"updated_at"`
	Contract          IntentLoopContract        `json:"contract"`
	Cycles            []CycleRecord             `json:"cycles"`
	CurrentCycle      int                       `json:"current_cycle"`
	MaxCycles         int                       `json:"max_cycles"`
	SeenFailureSets   map[string]bool           `json:"seen_failure_sets,omitempty"`
	BudgetTokens      int                       `json:"budget_tokens"`
	TokensUsed        int                       `json:"tokens_used"`
	BudgetExhausted   bool                      `json:"budget_exhausted,omitempty"`
	Status            intentloopcontract.Status `json:"status"`
	TerminationReason string                    `json:"termination_reason,omitempty"`
}

type CycleRecord struct {
	CycleNumber              int                            `json:"cycle_number"`
	PlanExecuteSessionID     string                         `json:"plan_execute_session_id"`
	CyclePromptPath          string                         `json:"cycle_prompt_path,omitempty"`
	CyclePromptSHA256        string                         `json:"cycle_prompt_sha256,omitempty"`
	Phase                    string                         `json:"phase"`
	Status                   intentloopcontract.CycleStatus `json:"status"`
	TerminalCompletionPath   string                         `json:"terminal_completion_path,omitempty"`
	ExitCode                 int                            `json:"exit_code"`
	FailureKind              string                         `json:"failure_kind,omitempty"`
	FailureReason            string                         `json:"failure_reason,omitempty"`
	ChangedPaths             []string                       `json:"changed_paths,omitempty"`
	MutationDeltas           []CycleMutationDelta           `json:"mutation_deltas,omitempty"`
	SourceSanityPassed       bool                           `json:"source_sanity_passed"`
	VerificationPassed       bool                           `json:"verification_passed"`
	ReviewPassed             bool                           `json:"review_passed"`
	RemainingFailures        []EvidenceGap                  `json:"remaining_failures,omitempty"`
	TokensUsed               int                            `json:"tokens_used,omitempty"`
	ResumeStatePath          string                         `json:"resume_state_path,omitempty"`
	PreRunScopedFingerprint  string                         `json:"pre_run_scoped_fingerprint,omitempty"`
	PostRunScopedFingerprint string                         `json:"post_run_scoped_fingerprint,omitempty"`
	ChildIdentity            *ChildRunIdentity              `json:"child_identity,omitempty"`
}

type EvidenceGap struct {
	Requirement     string                                `json:"requirement"`
	Status          string                                `json:"status"`
	Detail          string                                `json:"detail"`
	DetailHash      string                                `json:"detail_hash,omitempty"`
	Path            string                                `json:"path,omitempty"`
	Claim           *plancontract.AcceptanceEvidenceClaim `json:"claim,omitempty"`
	ThresholdResult string                                `json:"threshold_result,omitempty"`
}

type CycleMutationDelta struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	DiffSHA256 string `json:"diff_sha256,omitempty"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
}

type ChildRunIdentity struct {
	WorkspaceRootSHA256        string `json:"workspace_root_sha256,omitempty"`
	ScopeContractSHA256        string `json:"scope_contract_sha256,omitempty"`
	VerificationCommandsSHA256 string `json:"verification_commands_sha256,omitempty"`
	ToolManifestSHA256         string `json:"tool_manifest_sha256,omitempty"`
	BackendProfile             string `json:"backend_profile,omitempty"`
	PlannerModel               string `json:"planner_model,omitempty"`
	ExecutorModel              string `json:"executor_model,omitempty"`
}

func NewState(sessionID string, originalPrompt string, contract IntentLoopContract, maxCycles int, now time.Time) IntentLoopState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if maxCycles <= 0 {
		maxCycles = 5
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return IntentLoopState{
		SchemaVersion:   StateSchemaVersion,
		SessionID:       strings.TrimSpace(sessionID),
		OriginalPrompt:  originalPrompt,
		PromptSHA256:    sha256Hex(originalPrompt),
		CreatedAt:       stamp,
		UpdatedAt:       stamp,
		Contract:        contract,
		MaxCycles:       maxCycles,
		SeenFailureSets: map[string]bool{},
		Status:          intentloopcontract.StatusRunning,
	}
}

func SaveState(root string, state *IntentLoopState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("intent loop state is nil")
	}
	if err := normalizeStateForSave(state); err != nil {
		return "", err
	}

	fullPath, relPath, err := StatePath(root, state.SessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir intent loop state dir: %w", err)
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal intent loop state: %w", err)
	}

	if err := atomicfile.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write intent loop state: %w", err)
	}
	return relPath, nil
}

func LoadState(root string, sessionID string) (IntentLoopState, string, error) {
	fullPath, relPath, err := StatePath(root, sessionID)
	if err != nil {
		return IntentLoopState{}, "", err
	}
	body, err := os.ReadFile(fullPath)
	if err != nil {
		return IntentLoopState{}, relPath, fmt.Errorf("read intent loop state %s: %w", relPath, err)
	}

	var state IntentLoopState
	if err := json.Unmarshal(body, &state); err != nil {
		return IntentLoopState{}, relPath, fmt.Errorf("decode intent loop state %s: %w", relPath, err)
	}
	if state.SchemaVersion != StateSchemaVersion {
		return IntentLoopState{}, relPath, fmt.Errorf("unsupported intent loop schema %q in %s", state.SchemaVersion, relPath)
	}
	requestedSessionID := strings.TrimSpace(sessionID)
	loadedSessionID := strings.TrimSpace(state.SessionID)
	if loadedSessionID == "" {
		state.SessionID = requestedSessionID
	} else if loadedSessionID != requestedSessionID {
		return IntentLoopState{}, relPath, fmt.Errorf("intent loop state session id %q does not match requested session %q in %s", loadedSessionID, requestedSessionID, relPath)
	} else {
		state.SessionID = loadedSessionID
	}
	if !validIntentLoopSessionID.MatchString(state.SessionID) {
		return IntentLoopState{}, relPath, fmt.Errorf("invalid intent loop session id %q in %s", state.SessionID, relPath)
	}
	if state.SeenFailureSets == nil {
		state.SeenFailureSets = map[string]bool{}
	}
	if err := validateStateLifecycle(state, relPath); err != nil {
		return IntentLoopState{}, relPath, err
	}
	return state, relPath, nil
}

func StatePath(root string, sessionID string) (string, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !validIntentLoopSessionID.MatchString(sessionID) {
		return "", "", fmt.Errorf("invalid intent loop session id %q", sessionID)
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "intent-loop", "sessions", sessionID+".json"))
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}

func normalizeStateForSave(state *IntentLoopState) error {
	state.SessionID = strings.TrimSpace(state.SessionID)
	if state.SessionID == "" || !validIntentLoopSessionID.MatchString(state.SessionID) {
		return fmt.Errorf("invalid intent loop session id %q", state.SessionID)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = StateSchemaVersion
	}
	if state.SchemaVersion != StateSchemaVersion {
		return fmt.Errorf("unsupported intent loop schema %q", state.SchemaVersion)
	}
	if state.PromptSHA256 == "" {
		state.PromptSHA256 = sha256Hex(state.OriginalPrompt)
	}
	if state.CreatedAt == "" {
		state.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if state.SeenFailureSets == nil {
		state.SeenFailureSets = map[string]bool{}
	}
	if err := validateStateLifecycle(*state, ""); err != nil {
		return err
	}
	return nil
}

func validateStateLifecycle(state IntentLoopState, relPath string) error {
	status, ok := intentloopcontract.ParseStatus(string(state.Status))
	if !ok {
		if strings.TrimSpace(relPath) == "" {
			return fmt.Errorf("invalid intent loop status %q", state.Status)
		}
		return fmt.Errorf("invalid intent loop status %q in %s", state.Status, relPath)
	}

	dispatchedCycleIndex := -1
	for i, cycle := range state.Cycles {
		if _, ok := intentloopcontract.ParseCycleStatus(string(cycle.Status)); !ok {
			if strings.TrimSpace(relPath) == "" {
				return fmt.Errorf("invalid intent loop cycle status %q at cycles[%d]", cycle.Status, i)
			}
			return fmt.Errorf("invalid intent loop cycle status %q at cycles[%d] in %s", cycle.Status, i, relPath)
		}
		if cycle.Status != intentloopcontract.CycleStatusDispatched {
			continue
		}
		if dispatchedCycleIndex >= 0 {
			if strings.TrimSpace(relPath) == "" {
				return fmt.Errorf("invalid intent loop lifecycle with multiple dispatched cycles at cycles[%d] and cycles[%d]", dispatchedCycleIndex, i)
			}
			return fmt.Errorf("invalid intent loop lifecycle with multiple dispatched cycles at cycles[%d] and cycles[%d] in %s", dispatchedCycleIndex, i, relPath)
		}
		dispatchedCycleIndex = i
		if intentloopcontract.StatusTerminal(status) {
			if strings.TrimSpace(relPath) == "" {
				return fmt.Errorf("invalid intent loop lifecycle status %q with dispatched cycle at cycles[%d]", state.Status, i)
			}
			return fmt.Errorf("invalid intent loop lifecycle status %q with dispatched cycle at cycles[%d] in %s", state.Status, i, relPath)
		}
	}
	if dispatchedCycleIndex >= 0 && dispatchedCycleIndex != len(state.Cycles)-1 {
		if strings.TrimSpace(relPath) == "" {
			return fmt.Errorf("invalid intent loop lifecycle with non-current dispatched cycle at cycles[%d]", dispatchedCycleIndex)
		}
		return fmt.Errorf("invalid intent loop lifecycle with non-current dispatched cycle at cycles[%d] in %s", dispatchedCycleIndex, relPath)
	}

	return nil
}
