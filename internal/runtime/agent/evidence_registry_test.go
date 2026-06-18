package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- path evidence registry tests ---
func TestRegisterToolResultsUsesEvidenceOwnedDTO(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())

	registry.RegisterToolResults([]ToolResult{{
		ToolName: "read_file",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		Summary:  "Read internal/runtime/agent/evidence_registry.go",
	}})

	if !registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("expected body evidence from evidence-owned DTO")
	}
}

func TestCandidatePathExistenceResolvesLazily(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "runtime", "agent", "evidence_registry.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture path: %v", err)
	}
	if err := os.WriteFile(path, []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	registry := NewPathEvidenceRegistry(root)
	registry.RegisterToolResults([]ToolResult{{
		ToolName: "search_content",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		Summary:  "Found internal/runtime/agent/evidence_registry.go",
	}})

	if !registry.PathExists("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("expected candidate path existence to resolve lazily")
	}
	if registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("search candidate must not count as body evidence")
	}
}

// --- session evidence store tests ---
func TestFindMatchingPriorSessionFindsRecordsWrittenByStore(t *testing.T) {
	root := t.TempDir()
	prompt := "explain the runner evidence flow"
	objective := "objective with customer context"

	WriteSessionEvidenceRecord(root, "session-1", prompt, objective, []string{"b.go", "a.go", "a.go"}, false, "")

	record := FindMatchingPriorSession(root, prompt)
	if record == nil {
		t.Fatal("expected matching record written by WriteSessionEvidenceRecord")
	}

	if record.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", record.SessionID)
	}

	if len(record.ReadPaths) != 2 || record.ReadPaths[0] != "a.go" || record.ReadPaths[1] != "b.go" {
		t.Fatalf("ReadPaths = %#v, want sorted unique paths", record.ReadPaths)
	}
	if record.Prompt != "" || record.Objective != "" {
		t.Fatalf("raw prompt/objective should not be persisted: prompt=%q objective=%q", record.Prompt, record.Objective)
	}
	if record.PromptHash != sha256Hex(prompt) {
		t.Fatalf("PromptHash = %q, want hash of prompt", record.PromptHash)
	}
}

func TestFindMatchingPriorSessionReturnsMostRecentMatch(t *testing.T) {
	root := t.TempDir()
	prompt := "inspect source evidence"
	dir := sessionEvidenceDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir evidence dir: %v", err)
	}

	writeSessionRecordForTest(t, dir, SessionEvidenceRecord{
		SessionID:  "older",
		Prompt:     prompt,
		PromptHash: sha256Hex(prompt),
		ReadPaths:  []string{"older.go"},
		Timestamp:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	writeSessionRecordForTest(t, dir, SessionEvidenceRecord{
		SessionID:  "newer",
		Prompt:     prompt,
		PromptHash: sha256Hex(prompt),
		ReadPaths:  []string{"newer.go"},
		Timestamp:  time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})

	record := FindMatchingPriorSession(root, prompt)
	if record == nil {
		t.Fatal("expected matching record")
	}

	if record.SessionID != "newer" {
		t.Fatalf("SessionID = %q, want newer", record.SessionID)
	}
}

func writeSessionRecordForTest(t *testing.T, dir string, record SessionEvidenceRecord) {
	t.Helper()

	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, record.SessionID+".json"), body, 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

// --- evidence snapshot tests ---
func TestSnapshotPromoteReportsEvidenceDelta(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()
	if snapshot.ID() == "" {
		t.Fatal("snapshot ID is empty")
	}

	delta := snapshot.Promote(ToolResult{
		ToolName: "search_content",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		SidecarFacts: []EvidenceFact{
			{Key: "relationship.kind", Value: "candidate"},
		},
	})

	if len(delta.CandidatePaths) != 1 || delta.CandidatePaths[0] != "internal/runtime/agent/evidence_registry.go" {
		t.Fatalf("CandidatePaths delta = %#v", delta.CandidatePaths)
	}
	if snapshot.Version() != 1 {
		t.Fatalf("Version after first promote = %d, want 1", snapshot.Version())
	}
	if len(delta.Facts) != 1 || delta.Facts[0].Value != "candidate" {
		t.Fatalf("Facts delta = %#v", delta.Facts)
	}

	delta = snapshot.Promote(ToolResult{
		ToolName: "read_file",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
	})

	if len(delta.BodyReadPaths) != 1 || delta.BodyReadPaths[0] != "internal/runtime/agent/evidence_registry.go" {
		t.Fatalf("BodyReadPaths delta = %#v", delta.BodyReadPaths)
	}
	if snapshot.Version() != 2 {
		t.Fatalf("Version after second promote = %d, want 2", snapshot.Version())
	}

	body := snapshot.BodyRead("internal/runtime/agent/evidence_registry.go")
	if !body.BodyRead || body.EvidenceLevel < EvidenceBodyWindow {
		t.Fatalf("BodyRead evidence = %#v", body)
	}
}

func TestSnapshotFreezeDetachesReadModel(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()
	snapshot.Promote(ToolResult{ToolName: "search_files", Paths: []string{"a.go"}})

	frozen := snapshot.Freeze()
	if !frozen.Frozen() || frozen.ID() != snapshot.ID() || frozen.Version() != snapshot.Version() {
		t.Fatalf("unexpected frozen snapshot metadata: live=%#v frozen=%#v", snapshot, frozen)
	}

	snapshot.Promote(ToolResult{ToolName: "read_file", Paths: []string{"a.go"}})
	if snapshot.Version() != 2 {
		t.Fatalf("live snapshot version = %d, want 2", snapshot.Version())
	}
	if frozen.Version() != 1 {
		t.Fatalf("frozen snapshot version changed to %d", frozen.Version())
	}
	if frozen.BodyRead("a.go").BodyRead {
		t.Fatalf("frozen snapshot observed later live body read: %#v", frozen.BodyRead("a.go"))
	}

	if delta := frozen.Promote(ToolResult{ToolName: "read_file", Paths: []string{"b.go"}}); len(delta.BodyReadPaths) != 0 || frozen.Version() != 1 {
		t.Fatalf("frozen promote should be a no-op: delta=%#v version=%d", delta, frozen.Version())
	}
}

func TestSnapshotPromoteNoOpDoesNotAdvanceVersion(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()
	snapshot.Promote(ToolResult{ToolName: "search_files", Paths: []string{"a.go"}})

	if snapshot.Version() != 1 {
		t.Fatalf("initial version = %d, want 1", snapshot.Version())
	}
	delta := snapshot.Promote(ToolResult{ToolName: "search_files", Paths: []string{"a.go"}})
	if snapshot.Version() != 1 {
		t.Fatalf("duplicate promote advanced version to %d; delta=%#v", snapshot.Version(), delta)
	}
}

func TestSnapshotCandidatePaths(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()
	snapshot.Promote(ToolResult{ToolName: "search_files", Paths: []string{"b.go", "a.go"}})

	candidates := snapshot.CandidatePaths()
	if len(candidates) != 2 || candidates[0].Path != "a.go" || candidates[1].Path != "b.go" {
		t.Fatalf("CandidatePaths = %#v", candidates)
	}
}

func TestSnapshotPromoteCandidatePathFactDoesNotCountAsBodyRead(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()

	delta := snapshot.Promote(ToolResult{
		ToolName: "code_navigate",
		SidecarFacts: []EvidenceFact{
			{Key: "candidate_path", Value: "internal/runtime/agent/runner_core.go"},
		},
	})

	if len(delta.CandidatePaths) != 1 || delta.CandidatePaths[0] != "internal/runtime/agent/runner_core.go" {
		t.Fatalf("CandidatePaths delta = %#v", delta.CandidatePaths)
	}
	body := snapshot.BodyRead("internal/runtime/agent/runner_core.go")
	if body.BodyRead || body.EvidenceLevel != EvidenceNone {
		t.Fatalf("candidate path fact should not count as body evidence: %#v", body)
	}
}

func TestSnapshotPromoteTracksDesignFacets(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())
	snapshot := registry.Snapshot()

	delta := snapshot.Promote(ToolResult{
		ToolName: "read_file",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		SidecarFacts: []EvidenceFact{
			{Key: "named_path", Value: "snapshot=internal/runtime/agent/evidence_registry.go"},
			{Key: "pending_source_obligation", Value: "id=obl-source;path=internal/runtime/agent/evidence_registry.go"},
			{Key: "source_owner", Value: "path=internal/runtime/agent/evidence_registry.go;symbol=EvidenceSnapshot;role=mechanism;confidence=high"},
			{Key: "git_history", Value: "path=internal/runtime/agent/evidence_registry.go;satisfied=true;source=history_range"},
			{Key: "answerability.answerable", Value: "true"},
			{Key: "answerability.reason", Value: "source_body"},
			{Key: "answerability.path", Value: "internal/runtime/agent/evidence_registry.go"},
		},
	})

	if len(delta.NamedPaths) != 1 || delta.NamedPaths[0].Name != "snapshot" {
		t.Fatalf("NamedPaths delta = %#v", delta.NamedPaths)
	}
	if got := snapshot.NamedPaths(); len(got) != 1 || got[0].Path != "internal/runtime/agent/evidence_registry.go" {
		t.Fatalf("NamedPaths = %#v", got)
	}
	if got := snapshot.PendingSourceObligations(); len(got) != 1 || got[0].ID != "obl-source" {
		t.Fatalf("PendingSourceObligations = %#v", got)
	}
	if got := snapshot.SourceOwner("internal/runtime/agent/evidence_registry.go"); got.Symbol != "EvidenceSnapshot" || got.Role != "mechanism" {
		t.Fatalf("SourceOwner = %#v", got)
	}
	if got := snapshot.GitHistory("internal/runtime/agent/evidence_registry.go"); !got.Satisfied || got.Source != "history_range" {
		t.Fatalf("GitHistory = %#v", got)
	}
	if got := snapshot.ReadOnlyAnswerability(); !got.Answerable || got.Reason != "source_body" || len(got.Paths) != 1 {
		t.Fatalf("ReadOnlyAnswerability = %#v", got)
	}
}
