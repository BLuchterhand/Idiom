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

	report, err := compactChildArtifacts(root, &state)
	if err != nil {
		t.Fatalf("compactChildArtifacts error = %v", err)
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

func TestCompactStaleSessionsPreservesStateAndTerminalEvidence(t *testing.T) {
	root := t.TempDir()
	state := NewState("stale-session", "original", IntentLoopContract{}, 3, staleCleanupTime().Add(-10*24*time.Hour))
	terminalPath := writeRelativeFile(t, root, ".idiom/plan-execute/sessions/child-1.terminal.json", `{"status":"failed"}`)
	transientPath := writeRelativeFile(t, root, ".idiom/intent-loop/sessions/stale-session/transient/blob.cache", "duplicate")
	state.Cycles = []CycleRecord{{
		CycleNumber:            1,
		Status:                 "failed",
		PlanExecuteSessionID:   "child-1",
		TerminalCompletionPath: terminalPath,
		ChangedPaths:           []string{"a.go"},
		FailureKind:            "verification_failed",
		FailureReason:          "go test failed",
		RemainingFailures: []EvidenceGap{{
			Requirement: "verification:go test ./...",
			Status:      "failed",
			DetailHash:  "sha256:gap",
		}},
		ChildIdentity: &ChildRunIdentity{ExecutorModel: "executor"},
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	report, err := CompactStaleSessions(root, 7*24*time.Hour, time.Now().UTC().Add(10*24*time.Hour))
	if err != nil {
		t.Fatalf("CompactStaleSessions error = %v", err)
	}
	if !slices.Contains(report.CompactedSessions, "stale-session") {
		t.Fatalf("CompactedSessions = %#v", report.CompactedSessions)
	}
	if fileMissing(root, terminalPath) {
		t.Fatalf("terminal completion was removed: %s", terminalPath)
	}
	if !fileMissing(root, transientPath) {
		t.Fatalf("transient file still exists: %s", transientPath)
	}

	loaded, _, err := LoadState(root, "stale-session")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	cycle := loaded.Cycles[0]
	if cycle.TerminalCompletionPath != terminalPath || cycle.PlanExecuteSessionID != "child-1" ||
		cycle.FailureKind != "verification_failed" || len(cycle.RemainingFailures) != 1 ||
		cycle.ChildIdentity == nil || cycle.ChildIdentity.ExecutorModel != "executor" {
		t.Fatalf("compaction did not preserve audit metadata: %#v", cycle)
	}
}

func TestCompactStaleSessionsSkipsFreshSessions(t *testing.T) {
	root := t.TempDir()
	state := NewState("fresh-session", "original", IntentLoopContract{}, 3, staleCleanupTime())
	transientPath := writeRelativeFile(t, root, ".idiom/intent-loop/sessions/fresh-session/transient/blob.cache", "duplicate")
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	report, err := CompactStaleSessions(root, 7*24*time.Hour, staleCleanupTime())
	if err != nil {
		t.Fatalf("CompactStaleSessions error = %v", err)
	}
	if len(report.CompactedSessions) != 0 {
		t.Fatalf("fresh session compacted: %#v", report.CompactedSessions)
	}
	if fileMissing(root, transientPath) {
		t.Fatalf("fresh transient file was removed: %s", transientPath)
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
