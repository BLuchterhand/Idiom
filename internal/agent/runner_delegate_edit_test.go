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
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

// newDelegateEditTestWorkspace builds a workspace.Manager rooted at
// the given dir with writes enabled and the read-before-edit guard
// disabled. The guard exists in production to catch hallucinated
// paths but in unit tests we want to seed the file directly and
// dispatch edit_file without first reading it.
func newDelegateEditTestWorkspace(t *testing.T, root string) *workspace.Manager {
	t.Helper()
	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.SetBypassReadBeforeEdit(true)
	return mgr
}

func TestValidateDelegateEditRequestAcceptsSubstringShape(t *testing.T) {
	got := validateDelegateEditRequest(workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if got != nil {
		t.Fatalf("substring shape should be accepted, got rejection %#v", got)
	}
}

func TestValidateDelegateEditRequestAcceptsRangeShape(t *testing.T) {
	got := validateDelegateEditRequest(workspace.ToolRequest{
		Tool:      "delegate_edit",
		Path:      "a.txt",
		StartLine: 1,
		EndLine:   3,
		Content:   "after\n",
	})
	if got != nil {
		t.Fatalf("range shape should be accepted, got rejection %#v", got)
	}
}

func TestValidateDelegateEditRequestRejectsEmptyPath(t *testing.T) {
	got := validateDelegateEditRequest(workspace.ToolRequest{
		Tool:    "delegate_edit",
		OldText: "before",
		NewText: "after",
	})
	if got == nil || !strings.Contains(got.Error, "path") {
		t.Fatalf("empty path should be rejected with a path-named message, got %#v", got)
	}
}

func TestValidateDelegateEditRequestRejectsMissingShape(t *testing.T) {
	got := validateDelegateEditRequest(workspace.ToolRequest{
		Tool: "delegate_edit",
		Path: "a.txt",
	})
	if got == nil || !strings.Contains(got.Error, "shape") {
		t.Fatalf("missing edit shape should be rejected, got %#v", got)
	}
}

func TestParseDelegateEditReplyAcceptsBareJSON(t *testing.T) {
	reply := `{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`
	got, err := parseDelegateEditReply(reply, workspace.ToolRequest{Path: "a.txt"})
	if err != nil {
		t.Fatalf("bare JSON should parse, got %v", err)
	}
	if got.Tool != "edit_file" || got.Path != "a.txt" || got.OldText != "before" || got.NewText != "after" {
		t.Fatalf("parse result mismatch: %#v", got)
	}
}

func TestParseDelegateEditReplyHandlesJSONFence(t *testing.T) {
	reply := "```json\n{\"tool\":\"edit_file\",\"path\":\"a.txt\",\"old_text\":\"before\",\"new_text\":\"after\"}\n```"
	got, err := parseDelegateEditReply(reply, workspace.ToolRequest{Path: "a.txt"})
	if err != nil {
		t.Fatalf("fenced JSON should parse, got %v", err)
	}
	if got.Tool != "edit_file" {
		t.Fatalf("expected edit_file, got %q", got.Tool)
	}
}

func TestParseDelegateEditReplyRejectsUnknownTool(t *testing.T) {
	reply := `{"tool":"shell","command":"rm -rf /"}`
	_, err := parseDelegateEditReply(reply, workspace.ToolRequest{Path: "a.txt"})
	if err == nil || !strings.Contains(err.Error(), "disallowed tool") {
		t.Fatalf("non-write tool must be rejected, got %v", err)
	}
}

func TestParseDelegateEditReplyRejectsGarbage(t *testing.T) {
	reply := "I'm sorry, I can't do that."
	_, err := parseDelegateEditReply(reply, workspace.ToolRequest{Path: "a.txt"})
	if err == nil {
		t.Fatalf("prose reply must be rejected")
	}
}

func TestRunDelegateEditExecutorPathLandsTheEdit(t *testing.T) {
	// End-to-end: a stub executor returns a valid edit_file JSON for
	// the planner's spec; the runner dispatches it; the file on disk
	// is updated.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	calls := 0
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": `{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
				},
			}},
		})
	}))
	defer executor.Close()

	runner := &Runner{
		ImplementerClient: llama.NewClient(executor.URL, 2*time.Second),
		Workspace:         newDelegateEditTestWorkspace(t, root),
	}
	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
		Purpose: "rename the marker",
	})
	if calls != 1 {
		t.Fatalf("expected exactly one executor call, got %d", calls)
	}
	if result.Tool != "delegate_edit" {
		t.Fatalf("result tool must be delegate_edit, got %q", result.Tool)
	}
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("delegate_edit reported error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "delegated_via: executor") {
		t.Fatalf("result output should record the executor route, got %q", result.Output)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read after delegation: %v", err)
	}
	if string(got) != "after\n" {
		t.Fatalf("file should contain executor's edit, got %q", string(got))
	}
}

func TestRunDelegateEditFallsBackToDirectDispatchWhenExecutorErrors(t *testing.T) {
	// The executor returns 500; the runner must fall back to direct
	// dispatch so the lane still progresses with the planner's spec.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "executor down", http.StatusInternalServerError)
	}))
	defer executor.Close()

	runner := &Runner{
		ImplementerClient: llama.NewClient(executor.URL, 500*time.Millisecond),
		Workspace:         newDelegateEditTestWorkspace(t, root),
	}
	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("fallback dispatch should have applied the edit, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "delegated_via: direct") {
		t.Fatalf("fallback path should mark the route as direct, got %q", result.Output)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read after fallback: %v", err)
	}
	if string(got) != "after\n" {
		t.Fatalf("fallback should land the planner's spec, got %q", string(got))
	}
}

func TestRunDelegateEditWithoutExecutorClientFallsBackImmediately(t *testing.T) {
	// No ImplementerClient configured. Runner must NOT secretly use
	// r.Client (that defeats the cognitive-separation goal); it falls
	// back to direct dispatch.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	plannerCalls := 0
	planner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plannerCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "no"}}},
		})
	}))
	defer planner.Close()

	runner := &Runner{
		Client:    llama.NewClient(planner.URL, time.Second),
		Workspace: newDelegateEditTestWorkspace(t, root),
	}
	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if plannerCalls != 0 {
		t.Fatalf("planner client must not be reused as executor, got %d calls", plannerCalls)
	}
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("direct dispatch should have applied the edit, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "delegated_via: direct") {
		t.Fatalf("expected direct route marker, got %q", result.Output)
	}
}

func TestRunDelegateEditSurfacesValidationErrors(t *testing.T) {
	runner := &Runner{}
	got := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool: "delegate_edit",
		Path: "a.txt",
		// Missing both substring and range shapes.
	})
	if !strings.Contains(got.Error, "shape") {
		t.Fatalf("validation error should surface to the planner, got %#v", got)
	}
	if got.Tool != "delegate_edit" {
		t.Fatalf("validation result must keep delegate_edit tool name, got %q", got.Tool)
	}
}

func TestDelegateEditResultsAdvanceRequiredEditPathsProgress(t *testing.T) {
	// Critical wiring: a successful delegate_edit must register as a
	// changed path so required_edit_paths_progress ticks the path off.
	// Without this, the planner re-reads the progress checklist, sees
	// the path still ✗, and re-issues delegate_edit for the same path
	// — exactly the loop we're trying to prevent.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	runner := &Runner{
		Workspace: newDelegateEditTestWorkspace(t, root),
	}
	// No executor configured → direct fallback path produces an
	// edit_file result wrapped as delegate_edit.
	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if result.Tool != "delegate_edit" {
		t.Fatalf("delegate_edit wrap should keep tool=delegate_edit, got %q", result.Tool)
	}
	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		t.Fatalf("changedPathsFromToolResult must extract the edited path from a delegate_edit result; got %v", paths)
	}
	if !strings.Contains(paths[0], "a.txt") {
		t.Fatalf("extracted path should be a.txt, got %v", paths)
	}
}

func TestRunDelegateEditFallsBackOnUnparseableExecutorReply(t *testing.T) {
	// The executor returns prose. The runner falls back to direct
	// dispatch and emits a parse_error event.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Sorry, I can't help with that."},
			}},
		})
	}))
	defer executor.Close()

	var parseErrorEmitted bool
	runner := &Runner{
		ImplementerClient: llama.NewClient(executor.URL, time.Second),
		Workspace:         newDelegateEditTestWorkspace(t, root),
		OnEvent: func(ev RunEvent) {
			if ev.Role == "delegate_edit_parse_error" {
				parseErrorEmitted = true
			}
		},
	}
	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if !parseErrorEmitted {
		t.Fatalf("parse error event must be emitted when executor returns prose")
	}
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("fallback dispatch should still apply the edit, got error: %s", result.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read after fallback: %v", err)
	}
	if string(got) != "after\n" {
		t.Fatalf("fallback should land the planner's spec, got %q", string(got))
	}
}
