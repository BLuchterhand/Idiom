package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestRunNoteToolWritesHarnessMetadataAtomically(t *testing.T) {
	root := t.TempDir()
	result := runNoteTool(root, `{"key":"finding-1","content":"Important source fact."}`)
	if result.Error != "" {
		t.Fatalf("runNoteTool error: %s", result.Error)
	}
	path := filepath.Join(root, ".idiom", "notes", "finding-1.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if got := string(data); got != "Important source fact.\n" {
		t.Fatalf("note content = %q", got)
	}
}

func TestRunNoteToolRejectsUnsafeKeys(t *testing.T) {
	root := t.TempDir()
	keys := []string{"", "../x", "/abs", "a/b", `a\b`, ".hidden", strings.Repeat("a", noteToolMaxKeyLength+1), "snowman-☃"}
	for _, key := range keys {
		payload, _ := json.Marshal(map[string]string{"key": key, "content": "fact"})
		result := runNoteTool(root, string(payload))
		if result.Error == "" {
			t.Fatalf("expected key %q to be rejected, got %#v", key, result)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "x.md")); !os.IsNotExist(err) {
		t.Fatalf("unsafe traversal created file or returned unexpected error: %v", err)
	}
}

func TestRunNoteToolRejectsSymlinkedNotesDirectoryAndTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".idiom"), 0o700); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "notes")); err != nil {
		t.Fatalf("symlink notes: %v", err)
	}
	if result := runNoteTool(root, `{"key":"safe","content":"fact"}`); result.Error == "" {
		t.Fatalf("expected symlinked notes dir rejection, got %#v", result)
	}

	root = t.TempDir()
	noteDir := filepath.Join(root, ".idiom", "notes")
	if err := os.MkdirAll(noteDir, 0o700); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(noteDir, "safe.md")); err != nil {
		t.Fatalf("symlink target: %v", err)
	}
	if result := runNoteTool(root, `{"key":"safe","content":"fact"}`); result.Error == "" {
		t.Fatalf("expected symlink target rejection, got %#v", result)
	}
}

func TestWriteAutoFindingsNoteUsesHardenedWriter(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".idiom"), 0o700); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "notes")); err != nil {
		t.Fatalf("symlink notes: %v", err)
	}
	_, err := writeAutoFindingsNote(root, workerruntime.WorkerRuntimeBrief{Objective: "review note safety"}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/agent/runner.go"},
	})
	if err == nil {
		t.Fatalf("expected auto-findings note to reject symlinked notes dir")
	}
	if _, err := os.Stat(filepath.Join(outside, "auto-findings.md")); !os.IsNotExist(err) {
		t.Fatalf("auto-findings followed symlink or stat failed unexpectedly: %v", err)
	}
}

func TestRunNoteToolRejectsEmptyOrOversizedContent(t *testing.T) {
	root := t.TempDir()
	if result := runNoteTool(root, `{"key":"safe","content":"   "}`); result.Error == "" {
		t.Fatalf("expected empty content rejection")
	}
	payload, _ := json.Marshal(map[string]string{
		"key":     "safe",
		"content": strings.Repeat("x", noteToolMaxContentBytes+1),
	})
	if result := runNoteTool(root, string(payload)); result.Error == "" {
		t.Fatalf("expected oversized content rejection")
	}
}

func TestRunnerExecutesAllowedNoteTool(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		`{"tool":"note","key":"finding-1","content":"Observed Runner.Run note handling."}`,
		"Captured the finding.",
	}
	answer, events := runNoteToolScriptedConversation(t, root, replies, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "grounded_evidence_summary",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file"},
	})
	if answer != "Captured the finding." {
		t.Fatalf("answer = %q", answer)
	}
	if !slicesContainsString(events, "note_tool") {
		t.Fatalf("expected note_tool event, got %v", events)
	}
	data, err := os.ReadFile(filepath.Join(root, ".idiom", "notes", "finding-1.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(data), "Runner.Run note handling") {
		t.Fatalf("note content not written: %q", string(data))
	}
}

func TestRunnerDoesNotExposeNoteToolForDirectAnswer(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		`{"tool":"note","key":"finding-1","content":"should not write"}`,
		"Final answer after rejection.",
	}
	answer, events := runNoteToolScriptedConversation(t, root, replies, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "conversation_only",
	})
	if answer == "" {
		t.Fatalf("expected final answer")
	}
	if slicesContainsString(events, "note_tool") {
		t.Fatalf("direct_answer must not execute note tool, events=%v", events)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "notes", "finding-1.md")); !os.IsNotExist(err) {
		t.Fatalf("direct_answer note file exists or stat failed unexpectedly: %v", err)
	}
}

func runNoteToolScriptedConversation(t *testing.T, root string, replies []string, brief workerruntime.WorkerRuntimeBrief) (string, []string) {
	t.Helper()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	events := make([]string, 0)
	runner := &Runner{
		Client:       llama.NewClient(server.URL, time.Second),
		Workspace:    mgr,
		RuntimeBrief: brief,
		Policy: RunPolicy{
			MaxToolSteps:       4,
			MaxNoProgressTurns: 4,
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event.Role)
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "capture a finding"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return answer, events
}

func slicesContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
