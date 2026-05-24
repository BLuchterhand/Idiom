package planintent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workspace"
)

const (
	CodeMissingSourceEvidence = "missing_source_evidence"
	CodeAmbiguousAnchor       = "ambiguous_anchor"
	CodeNonUniqueAnchor       = "non_unique_anchor"
	CodeScopeRejected         = "scope_rejected"
	CodeUnsupportedOperation  = "unsupported_operation"
	CodeIntentBlocker         = "intent_blocker"
	CodeCompilerBug           = "compiler_bug"
)

type CompileOptions struct {
	WorkspaceRoot string
	Contract      Contract
}

type CompileError struct {
	Code        string
	IntentIndex int
	Path        string
	Message     string
}

func (err *CompileError) Error() string {
	if err == nil {
		return ""
	}
	path := strings.TrimSpace(err.Path)
	if path == "" {
		return fmt.Sprintf("%s: intents[%d]: %s", err.Code, err.IntentIndex, err.Message)
	}
	return fmt.Sprintf("%s: intents[%d] %s: %s", err.Code, err.IntentIndex, path, err.Message)
}

func Compile(artifact Artifact, opts CompileOptions) ([]workspace.PlanEdit, error) {
	if strings.TrimSpace(opts.WorkspaceRoot) == "" {
		return nil, &CompileError{Code: CodeCompilerBug, IntentIndex: -1, Message: "workspace root is required"}
	}
	if err := Validate(artifact, opts.Contract); err != nil {
		return nil, err
	}
	if len(artifact.Intents) == 0 {
		return nil, &CompileError{Code: CodeIntentBlocker, IntentIndex: -1, Message: "edit intent contains only typed blocker(s); no candidate plan edits were compiled"}
	}
	edits := make([]workspace.PlanEdit, 0, len(artifact.Intents))
	for i, intent := range artifact.Intents {
		edit, err := compileIntent(i, intent, opts.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		if edit.Path != "" {
			edits = append(edits, edit)
		}
	}
	return edits, nil
}

func compileIntent(index int, intent Intent, root string) (workspace.PlanEdit, error) {
	path, err := normalizePath(intent.Path)
	if err != nil {
		return workspace.PlanEdit{}, compileError(CodeScopeRejected, index, intent.Path, err.Error())
	}
	switch intent.Operation {
	case OperationReplaceExactText:
		return compileReplaceExactText(index, path, intent, root)
	case OperationInsertAfterLineAnchor:
		return compileInsertAfterLineAnchor(index, path, intent, root)
	case OperationAddNewFile:
		return compileAddNewFile(index, path, intent, root)
	default:
		return workspace.PlanEdit{}, compileError(CodeUnsupportedOperation, index, path, fmt.Sprintf("%s is valid edit_intent.v1 but not supported by the deterministic compiler MVP", intent.Operation))
	}
}

func compileReplaceExactText(index int, path string, intent Intent, root string) (workspace.PlanEdit, error) {
	current, err := readWorkspaceFile(root, path)
	if err != nil {
		return workspace.PlanEdit{}, compileError(CodeMissingSourceEvidence, index, path, err.Error())
	}
	matches := strings.Count(current, intent.OldText)
	if matches == 0 {
		return workspace.PlanEdit{}, compileError(CodeAmbiguousAnchor, index, path, "old_text does not appear in current source")
	}
	if matches > 1 {
		return workspace.PlanEdit{}, compileError(CodeNonUniqueAnchor, index, path, fmt.Sprintf("old_text matched %d locations", matches))
	}
	return workspace.PlanEdit{
		Path:    path,
		OldText: intent.OldText,
		NewText: intent.NewText,
		Purpose: intentPurpose(intent, "replace exact text from edit intent"),
	}, nil
}

func compileInsertAfterLineAnchor(index int, path string, intent Intent, root string) (workspace.PlanEdit, error) {
	current, err := readWorkspaceFile(root, path)
	if err != nil {
		return workspace.PlanEdit{}, compileError(CodeMissingSourceEvidence, index, path, err.Error())
	}
	line, matches := findUniqueLineAnchor(current, intent.LineAnchor)
	if matches == 0 {
		return workspace.PlanEdit{}, compileError(CodeAmbiguousAnchor, index, path, "line_anchor does not appear in current source")
	}
	if matches > 1 {
		return workspace.PlanEdit{}, compileError(CodeNonUniqueAnchor, index, path, fmt.Sprintf("line_anchor matched %d lines", matches))
	}
	return workspace.PlanEdit{
		Path:            path,
		InsertAfterLine: line,
		Content:         intent.Content,
		Purpose:         intentPurpose(intent, "insert after line anchor from edit intent"),
	}, nil
}

func compileAddNewFile(index int, path string, intent Intent, root string) (workspace.PlanEdit, error) {
	abs, err := workspacePath(root, path)
	if err != nil {
		return workspace.PlanEdit{}, compileError(CodeScopeRejected, index, path, err.Error())
	}
	if _, err := os.Stat(abs); err == nil {
		return workspace.PlanEdit{}, compileError(CodeScopeRejected, index, path, "add_new_file target already exists")
	} else if !os.IsNotExist(err) {
		return workspace.PlanEdit{}, compileError(CodeMissingSourceEvidence, index, path, err.Error())
	}
	return workspace.PlanEdit{
		Path:       path,
		NewText:    intent.NewFileContent,
		ReplaceAll: true,
		Purpose:    intentPurpose(intent, "add new file from edit intent"),
	}, nil
}

func readWorkspaceFile(root string, path string) (string, error) {
	abs, err := workspacePath(root, path)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func workspacePath(root string, path string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("workspace root is invalid: %w", err)
	}
	cleanPath, err := normalizePath(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(cleanRoot, filepath.FromSlash(cleanPath)))
	if err != nil {
		return "", fmt.Errorf("path is invalid: %w", err)
	}
	if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path must stay relative to the workspace")
	}
	return abs, nil
}

func findUniqueLineAnchor(content string, anchor string) (int, int) {
	anchor = strings.TrimRight(anchor, "\r\n")
	lineNumber := 0
	matches := 0
	for i, line := range strings.Split(content, "\n") {
		if strings.TrimRight(line, "\r") != anchor {
			continue
		}
		lineNumber = i + 1
		matches++
	}
	return lineNumber, matches
}

func intentPurpose(intent Intent, fallback string) string {
	if purpose := strings.TrimSpace(intent.Purpose); purpose != "" {
		return purpose
	}
	if behavior := strings.TrimSpace(intent.DesiredBehavior); behavior != "" {
		return behavior
	}
	return fallback
}

func compileError(code string, index int, path string, message string) error {
	return &CompileError{Code: code, IntentIndex: index, Path: path, Message: message}
}
