package workspace

import (
	"strings"
	"testing"
)

func TestParseToolRequestExtractsFirstJSONObjectFromToolFence(t *testing.T) {
	req, raw, ok, err := ParseToolRequest("before\n```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```\nafter")
	if err != nil {
		t.Fatalf("ParseToolRequest: %v", err)
	}
	if !ok {
		t.Fatal("expected tool request")
	}
	if req.Tool != "read_file" || req.Path != "README.md" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if raw != `{"tool":"read_file","path":"README.md"}` {
		t.Fatalf("unexpected raw payload: %q", raw)
	}
}

func TestParseToolRequestRejectsMalformedOrMissingTool(t *testing.T) {
	_, _, ok, err := ParseToolRequest("no tool here")
	if err != nil || ok {
		t.Fatalf("expected no request, ok=%t err=%v", ok, err)
	}
	_, _, ok, err = ParseToolRequest("```tool\n{\"path\":\"README.md\"}\n```")
	if !ok || err == nil || !strings.Contains(err.Error(), "missing tool") {
		t.Fatalf("expected missing tool error, ok=%t err=%v", ok, err)
	}
}

func TestExploreShellClassificationAndValidation(t *testing.T) {
	if !IsBroadInspectionRequest(ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"}) {
		t.Fatal("expected find shell command to be broad inspection")
	}
	if !IsNarrowingRequest(ToolRequest{Tool: "shell", Command: "rg TODO internal"}) {
		t.Fatal("expected rg shell command to be narrowing")
	}
	if err := validateExploreShellCommand("git status --short"); err != nil {
		t.Fatalf("expected git status to be allowed: %v", err)
	}
	for _, command := range []string{"git checkout main", "python3 script.py", "rg TODO > out.txt", "cat $(pwd)"} {
		if err := validateExploreShellCommand(command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
	}
}

func TestShouldClearBroadInspectionLockRequiresRelevantSuccessfulNarrowing(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())
	if !mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content", Pattern: "route"}, ToolResult{Summary: "ok"}) {
		t.Fatal("search_content should clear broad inspection lock")
	}
	if !mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}, ToolResult{Summary: "ok"}) {
		t.Fatal("scoped list_files should clear broad inspection lock")
	}
	if mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content"}, ToolResult{Error: "failed"}) {
		t.Fatal("failed tool result should not clear broad inspection lock")
	}
	if mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files"}, ToolResult{Summary: "ok"}) {
		t.Fatal("broad inspection should not clear broad inspection lock")
	}
}

func TestScopedListFilesIsNarrowingNotBroadInspection(t *testing.T) {
	if !IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "."}) {
		t.Fatal("root list_files should be broad inspection")
	}
	if IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}) {
		t.Fatal("scoped list_files should not be broad inspection")
	}
	if !IsNarrowingRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}) {
		t.Fatal("scoped list_files should be a narrowing request")
	}
}
