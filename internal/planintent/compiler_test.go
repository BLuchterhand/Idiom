package planintent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

func TestCompileReplaceExactTextProducesSubmitPlanCandidate(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")
	artifact := validReplaceArtifact()
	artifact.Intents[0].OldText = "return \"old\""
	artifact.Intents[0].NewText = "return \"next\""

	edits := compileOK(t, root, artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	if len(edits) != 1 || edits[0].Path != "internal/a.go" || edits[0].OldText != "return \"old\"" || edits[0].NewText != "return \"next\"" {
		t.Fatalf("unexpected edits: %#v", edits)
	}
	assertSubmitPlanAccepts(t, root, edits)
}

func TestCompileInsertAfterLineAnchorProducesSubmitPlanCandidate(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")
	artifact := Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []Intent{{
			Path:       "internal/a.go",
			Operation:  OperationInsertAfterLineAnchor,
			LineAnchor: "func label() string {",
			Content:    "\tvalue := \"next\"",
			Purpose:    "insert local value",
		}},
	}

	edits := compileOK(t, root, artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	if len(edits) != 1 || edits[0].InsertAfterLine != 3 || edits[0].Content != "\tvalue := \"next\"" {
		t.Fatalf("unexpected insert edit: %#v", edits)
	}
	assertSubmitPlanAccepts(t, root, edits)
}

func TestCompileAddNewFileProducesSubmitPlanCandidate(t *testing.T) {
	root := t.TempDir()
	artifact := Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		Scope:         Scope{RequiredEditPaths: []string{"internal/new.go"}},
		Intents: []Intent{{
			Path:           "internal/new.go",
			Operation:      OperationAddNewFile,
			NewFileContent: "package internal\n\nfunc NewLabel() string { return \"new\" }\n",
			Purpose:        "add new label file",
		}},
	}

	edits := compileOK(t, root, artifact, Contract{RequiredEditPaths: []string{"internal/new.go"}})

	if len(edits) != 1 || !edits[0].ReplaceAll || edits[0].NewText == "" {
		t.Fatalf("unexpected new-file edit: %#v", edits)
	}
	assertSubmitPlanAccepts(t, root, edits)
}

func TestCompileBlocksNonUniqueExactAnchor(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/a.go", "old\nold\n")
	artifact := validReplaceArtifact()
	artifact.Intents[0].OldText = "old"
	artifact.Intents[0].NewText = "new"

	_, err := Compile(artifact, CompileOptions{
		WorkspaceRoot: root,
		Contract:      Contract{RequiredEditPaths: []string{"internal/a.go"}},
	})

	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected compile error, got %v", err)
	}
	if compileErr.Code != CodeNonUniqueAnchor {
		t.Fatalf("Code = %q, want %q", compileErr.Code, CodeNonUniqueAnchor)
	}
}

func TestCompileBlocksMissingSourceFile(t *testing.T) {
	root := t.TempDir()
	artifact := validReplaceArtifact()
	artifact.Intents[0].OldText = "old"
	artifact.Intents[0].NewText = "new"

	_, err := Compile(artifact, CompileOptions{
		WorkspaceRoot: root,
		Contract:      Contract{RequiredEditPaths: []string{"internal/a.go"}},
	})

	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected compile error, got %v", err)
	}
	if compileErr.Code != CodeMissingSourceEvidence {
		t.Fatalf("Code = %q, want %q", compileErr.Code, CodeMissingSourceEvidence)
	}
}

func TestCompileBlocksMissingLineAnchor(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/a.go", "package internal\n")
	artifact := Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []Intent{{
			Path:       "internal/a.go",
			Operation:  OperationInsertAfterLineAnchor,
			LineAnchor: "func label() string {",
			Content:    "\treturn \"next\"",
		}},
	}

	_, err := Compile(artifact, CompileOptions{
		WorkspaceRoot: root,
		Contract:      Contract{RequiredEditPaths: []string{"internal/a.go"}},
	})

	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected compile error, got %v", err)
	}
	if compileErr.Code != CodeAmbiguousAnchor {
		t.Fatalf("Code = %q, want %q", compileErr.Code, CodeAmbiguousAnchor)
	}
}

func TestCompileBlocksUnsupportedVocabularyUntilCompilerOwnsIt(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/a.go", "package internal\n")
	artifact := validReplaceArtifact()
	artifact.Intents[0].Operation = OperationReplaceSymbolBody
	artifact.Intents[0].Symbol = "label"
	artifact.Intents[0].DesiredBehavior = "return next label"

	_, err := Compile(artifact, CompileOptions{
		WorkspaceRoot: root,
		Contract:      Contract{RequiredEditPaths: []string{"internal/a.go"}},
	})

	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected compile error, got %v", err)
	}
	if compileErr.Code != CodeUnsupportedOperation {
		t.Fatalf("Code = %q, want %q", compileErr.Code, CodeUnsupportedOperation)
	}
}

func TestCompileDoesNotTurnTypedBlockerIntoEmptyPlan(t *testing.T) {
	root := t.TempDir()
	artifact := Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Blockers: []Blocker{{
			Code:    "needs_source_evidence",
			Message: "current source body is missing",
			Paths:   []string{"internal/a.go"},
		}},
	}

	_, err := Compile(artifact, CompileOptions{
		WorkspaceRoot: root,
		Contract:      Contract{RequiredEditPaths: []string{"internal/a.go"}},
	})

	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected compile error, got %v", err)
	}
	if compileErr.Code != CodeIntentBlocker {
		t.Fatalf("Code = %q, want %q", compileErr.Code, CodeIntentBlocker)
	}
}

func compileOK(t *testing.T, root string, artifact Artifact, contract Contract) []workspace.PlanEdit {
	t.Helper()
	edits, err := Compile(artifact, CompileOptions{WorkspaceRoot: root, Contract: contract})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return edits
}

func assertSubmitPlanAccepts(t *testing.T, root string, edits []workspace.PlanEdit) {
	t.Helper()
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "candidate compiled from non-executable edit intent",
		Edits:     edits,
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected compiled edits: %#v", result)
	}
}

func writeFixture(t *testing.T, root string, path string, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir fixture parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
