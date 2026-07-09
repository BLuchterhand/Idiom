package intentloop

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestCompactChildArtifactsPreservesEvidenceAndDeletesOnlyTransientFiles(t *testing.T) {
	root := t.TempDir()
	state := NewState("cleanup-session", "original", IntentLoopContract{}, 3, staleCleanupTime())
	terminalPath := writeRelativeFile(t, root, ".idiom/plan-execute/sessions/child-1.terminal.json", `{"status":"completed"}`)
	progressPath := writeRelativeFile(t, root, ".idiom/plan-execute/progress/child-1.json", `{"status":"running"}`)
	transientPath := writeRelativeFile(t, root, ".idiom/intent-loop/sessions/cleanup-session/spill/output.tmp", "large duplicate")
	promptPath := writeRelativeFile(t, root, ".idiom/intent-loop/sessions/cleanup-session/cycle-001.prompt.md", "prompt")
	state.Cycles = []CycleRecord{{
		CycleNumber:            1,
		Status:                 "completed",
		PlanExecuteSessionID:   "child-1",
		CyclePromptPath:        promptPath,
		TerminalCompletionPath: terminalPath,
		ResumeStatePath:        progressPath,
		ChangedPaths:           []string{"internal/workflows/intentloop/resume.go"},
		FailureKind:            "verification_failed",
		MutationDeltas: []CycleMutationDelta{{
			Path:       "internal/workflows/intentloop/resume.go",
			Kind:       "modify",
			DiffSHA256: "sha256:diff",
		}},
		ChildIdentity: &ChildRunIdentity{BackendProfile: "profile"},
	}}

	report, err := CompactChildArtifacts(root, &state)
	if err != nil {
		t.Fatalf("CompactChildArtifacts error = %v", err)
	}
	if fileMissing(root, terminalPath) || fileMissing(root, progressPath) || fileMissing(root, promptPath) {
		t.Fatalf("cleanup removed preserved evidence: terminal=%v progress=%v prompt=%v",
			fileMissing(root, terminalPath), fileMissing(root, progressPath), fileMissing(root, promptPath))
	}
	if !fileMissing(root, transientPath) {
		t.Fatalf("transient file still exists: %s", transientPath)
	}
	if len(report.ChildSnapshots) != 1 {
		t.Fatalf("ChildSnapshots = %#v", report.ChildSnapshots)
	}
	snapshot := report.ChildSnapshots[0]
	if snapshot.TerminalCompletionSHA256 == "" || snapshot.ResumeStateSHA256 == "" {
		t.Fatalf("snapshot missing evidence hashes: %#v", snapshot)
	}
	if !slices.Contains(report.PreservedPaths, terminalPath) || !slices.Contains(report.PreservedPaths, progressPath) {
		t.Fatalf("preserved paths missing evidence: %#v", report.PreservedPaths)
	}
	if !slices.Contains(report.DeletedPaths, ".idiom/intent-loop/sessions/cleanup-session/spill") {
		t.Fatalf("deleted paths missing transient spill dir: %#v", report.DeletedPaths)
	}
	if state.Cycles[0].ChangedPaths[0] == "" || state.Cycles[0].MutationDeltas[0].DiffSHA256 == "" || state.Cycles[0].ChildIdentity == nil {
		t.Fatalf("parent metadata was not preserved: %#v", state.Cycles[0])
	}
}

func writeRelativeFile(t *testing.T, root string, relPath string, body string) string {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return relPath
}

func fileMissing(root string, relPath string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath)))
	return os.IsNotExist(err)
}

func staleCleanupTime() time.Time {
	return time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
}
