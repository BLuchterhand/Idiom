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

func TestPathEvidenceRegistryRejectsAbsoluteAndEscapingBodyReadPaths(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())

	registry.RegisterToolResults([]ToolResult{{
		ToolName: "read_file",
		Paths: []string{
			"/tmp/outside.go",
			`C:\tmp\outside.go`,
			"C:/tmp/outside.go",
			"../outside.go",
			"internal/runtime/agent/evidence_registry.go",
		},
		Summary: "Read internal/runtime/agent/evidence_registry.go",
	}})

	if registry.HasBodyEvidence("/tmp/outside.go") {
		t.Fatal("absolute body-read path must not be accepted as workspace evidence")
	}
	if registry.HasBodyEvidence("../outside.go") {
		t.Fatal("parent-escaping body-read path must not be accepted as workspace evidence")
	}
	if registry.HasBodyEvidence(`C:\tmp\outside.go`) || registry.HasBodyEvidence("C:/tmp/outside.go") {
		t.Fatal("Windows absolute body-read paths must not be accepted as workspace evidence")
	}
	if !registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("repo-local body-read path should still be accepted")
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

	if !registry.EnsurePathExists("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("expected candidate path existence to resolve lazily")
	}
	if registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("search candidate must not count as body evidence")
	}
}

// --- session evidence store tests ---
func TestFindMatchingPriorSessionFindsPromptHashRecord(t *testing.T) {
	root := t.TempDir()
	prompt := "explain the runner evidence flow"
	dir := sessionEvidenceDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir evidence dir: %v", err)
	}

	writeSessionRecordForTest(t, dir, SessionEvidenceRecord{
		SessionID:  "session-1",
		PromptHash: sha256Hex(prompt),
		ReadPaths:  []string{"a.go", "b.go"},
		Timestamp:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})

	record := FindMatchingPriorSession(root, prompt)
	if record == nil {
		t.Fatal("expected matching prompt-hash record")
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

func TestFindMatchingPriorSessionFallsBackToLegacyPromptText(t *testing.T) {
	root := t.TempDir()
	prompt := "inspect legacy source evidence"
	dir := sessionEvidenceDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir evidence dir: %v", err)
	}

	writeSessionRecordForTest(t, dir, SessionEvidenceRecord{
		SessionID: "legacy",
		Prompt:    prompt,
		ReadPaths: []string{"legacy.go"},
		Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})

	record := FindMatchingPriorSession(root, prompt)
	if record == nil {
		t.Fatal("expected legacy raw-prompt record")
	}
	if record.SessionID != "legacy" || len(record.ReadPaths) != 1 || record.ReadPaths[0] != "legacy.go" {
		t.Fatalf("legacy record = %#v", record)
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
