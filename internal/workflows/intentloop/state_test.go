package intentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"
)

func TestStateJSONRoundTripPreservesCycleArtifacts(t *testing.T) {
	root := t.TempDir()
	contract := IntentLoopContract{AtomID: "impl"}
	state := NewState("session-1", "original prompt", contract, 3, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      ".idiom/intent-loop/sessions/session-1/cycle-001.prompt.md",
		CyclePromptSHA256:    "prompt-hash",
		Phase:                "planner",
		Status:               "completed",
		ChangedPaths:         []string{"internal/foo/foo.go"},
		MutationDeltas: []CycleMutationDelta{{
			Path:       "internal/foo/foo.go",
			Kind:       "modify",
			DiffSHA256: "sha256:diff",
			Additions:  3,
			Deletions:  1,
		}},
		RemainingFailures: []EvidenceGap{{
			Requirement: "verification:go vet ./...",
			Status:      "failed",
			Detail:      "vet failed",
			DetailHash:  sha256Prefixed("vet failed with full details"),
		}},
		ChildIdentity: &ChildRunIdentity{
			WorkspaceRootSHA256:        "root",
			ScopeContractSHA256:        "scope",
			VerificationCommandsSHA256: "commands",
			ToolManifestSHA256:         "tools",
			BackendProfile:             "profile",
			PlannerModel:               "planner",
			ExecutorModel:              "executor",
		},
	}}
	state.SeenFailureSets[GapFingerprint(state.Cycles[0].RemainingFailures)] = true

	relPath, err := SaveState(root, &state)
	if err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	if relPath != ".idiom/intent-loop/sessions/session-1.json" {
		t.Fatalf("relPath = %q", relPath)
	}

	loaded, loadedPath, err := LoadState(root, "session-1")
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if loadedPath != relPath {
		t.Fatalf("loadedPath = %q, want %q", loadedPath, relPath)
	}
	if loaded.PromptSHA256 != sha256Hex("original prompt") {
		t.Fatalf("PromptSHA256 = %q", loaded.PromptSHA256)
	}
	if len(loaded.Cycles) != 1 || len(loaded.Cycles[0].MutationDeltas) != 1 {
		t.Fatalf("loaded cycles = %#v", loaded.Cycles)
	}
	if loaded.Cycles[0].MutationDeltas[0].DiffSHA256 != "sha256:diff" {
		t.Fatalf("mutation delta hash not retained: %#v", loaded.Cycles[0].MutationDeltas[0])
	}
	if loaded.Cycles[0].ChildIdentity == nil || loaded.Cycles[0].ChildIdentity.ExecutorModel != "executor" {
		t.Fatalf("child identity not retained: %#v", loaded.Cycles[0].ChildIdentity)
	}
	if len(loaded.SeenFailureSets) != 1 {
		t.Fatalf("SeenFailureSets = %#v", loaded.SeenFailureSets)
	}
}

func TestLoadStateRejectsInvalidSessionID(t *testing.T) {
	if _, _, err := StatePath(t.TempDir(), "../bad"); err == nil {
		t.Fatal("StatePath accepted invalid session id")
	}
}

func TestLoadStateRejectsEmbeddedSessionMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewState("other-session", "prompt", IntentLoopContract{}, 1, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))

	fullPath, _, err := StatePath(root, "requested-session")
	if err != nil {
		t.Fatalf("StatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("write mismatched state: %v", err)
	}

	_, _, err = LoadState(root, "requested-session")
	if err == nil {
		t.Fatal("LoadState accepted embedded session mismatch")
	}
}

func TestLoadStateRejectsUnknownStatus(t *testing.T) {
	root := t.TempDir()
	state := NewState("bad-status", "prompt", IntentLoopContract{}, 1, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Status = intentloopcontract.Status("typo")
	writeIntentLoopStateForTest(t, root, state)

	_, _, err := LoadState(root, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "invalid intent loop status") {
		t.Fatalf("expected invalid status error, got %v", err)
	}
}

func TestLoadStateRejectsUnknownCycleStatus(t *testing.T) {
	root := t.TempDir()
	state := NewState("bad-cycle-status", "prompt", IntentLoopContract{}, 1, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		PlanExecuteSessionID: "child",
		Status:               intentloopcontract.CycleStatus("typo"),
	}}
	writeIntentLoopStateForTest(t, root, state)

	_, _, err := LoadState(root, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "invalid intent loop cycle status") {
		t.Fatalf("expected invalid cycle status error, got %v", err)
	}
}

func TestLoadStateRejectsTerminalStatusWithDispatchedCycle(t *testing.T) {
	root := t.TempDir()
	state := NewState("terminal-with-dispatched-child", "prompt", IntentLoopContract{}, 1, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Status = intentloopcontract.StatusCompleted
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		PlanExecuteSessionID: "child",
		Status:               intentloopcontract.CycleStatusDispatched,
	}}
	writeIntentLoopStateForTest(t, root, state)

	_, _, err := LoadState(root, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "invalid intent loop lifecycle") {
		t.Fatalf("expected invalid lifecycle error, got %v", err)
	}
}

func TestLoadStateRejectsMultipleDispatchedCycles(t *testing.T) {
	root := t.TempDir()
	state := NewState("multiple-dispatched-children", "prompt", IntentLoopContract{}, 2, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Cycles = []CycleRecord{
		{CycleNumber: 1, PlanExecuteSessionID: "child-1", Status: intentloopcontract.CycleStatusDispatched},
		{CycleNumber: 2, PlanExecuteSessionID: "child-2", Status: intentloopcontract.CycleStatusDispatched},
	}
	writeIntentLoopStateForTest(t, root, state)

	_, _, err := LoadState(root, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "multiple dispatched cycles") {
		t.Fatalf("expected multiple dispatched lifecycle error, got %v", err)
	}
}

func TestLoadStateRejectsNonCurrentDispatchedCycle(t *testing.T) {
	root := t.TempDir()
	state := NewState("non-current-dispatched-child", "prompt", IntentLoopContract{}, 2, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Cycles = []CycleRecord{
		{CycleNumber: 1, PlanExecuteSessionID: "child-1", Status: intentloopcontract.CycleStatusDispatched},
		{CycleNumber: 2, PlanExecuteSessionID: "child-2", Status: intentloopcontract.CycleStatusCompleted},
	}
	writeIntentLoopStateForTest(t, root, state)

	_, _, err := LoadState(root, state.SessionID)
	if err == nil || !strings.Contains(err.Error(), "non-current dispatched cycle") {
		t.Fatalf("expected non-current dispatched lifecycle error, got %v", err)
	}
}

func TestSaveStateRejectsTerminalStatusWithDispatchedCycle(t *testing.T) {
	state := NewState("save-terminal-with-dispatched-child", "prompt", IntentLoopContract{}, 1, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	state.Status = intentloopcontract.StatusBlocked
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		PlanExecuteSessionID: "child",
		Status:               intentloopcontract.CycleStatusDispatched,
	}}

	if _, err := SaveState(t.TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid intent loop lifecycle") {
		t.Fatalf("expected invalid lifecycle error, got %v", err)
	}
}

func writeIntentLoopStateForTest(t *testing.T, root string, state IntentLoopState) {
	t.Helper()

	fullPath, _, err := StatePath(root, state.SessionID)
	if err != nil {
		t.Fatalf("StatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}
