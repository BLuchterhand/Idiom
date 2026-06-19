package intentloop

import (
	"testing"
	"time"
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
