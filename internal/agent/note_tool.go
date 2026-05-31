package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

const (
	noteToolName            = "note"
	noteToolMaxKeyLength    = 80
	noteToolMaxContentBytes = 64 * 1024
)

var noteToolKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type noteToolArgs struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

func noteToolDefinition() llama.Tool {
	return llama.Tool{
		Type: "function",
		Function: llama.ToolFunction{
			Name:        noteToolName,
			Description: "Record an intermediate finding as harness scratch metadata. Notes are stored under .idiom/notes/ and never count as workspace mutations or task evidence.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"ASCII slug for this finding, using letters, numbers, dot, underscore, or dash; no paths."},"content":{"type":"string","description":"What you learned. Include file paths, function names, line numbers, and uncertainty."}},"required":["key","content"]}`),
		},
	}
}

func runNoteTool(workspaceRoot string, raw string) workspace.ToolResult {
	result := workspace.ToolResult{Tool: noteToolName}
	var args noteToolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		result.Summary = "Note rejected: invalid arguments"
		result.Error = "note requires JSON arguments with non-empty key and content"
		return result
	}
	key, err := validateNoteToolKey(args.Key)
	if err != nil {
		result.Summary = "Note rejected: invalid key"
		result.Error = err.Error()
		return result
	}
	content, err := validateNoteToolContent(args.Content)
	if err != nil {
		result.Summary = "Note rejected: invalid content"
		result.Error = err.Error()
		return result
	}
	relPath, err := writeNoteToolFile(workspaceRoot, key, content)
	if err != nil {
		result.Summary = "Note failed: write rejected"
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("Note saved: %s", key)
	result.Output = fmt.Sprintf("Recorded finding %q at %s (%d bytes)", key, relPath, len([]byte(content)))
	return result
}

func writeAutoFindingsNote(workspaceRoot string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, error) {
	var b strings.Builder
	b.WriteString("# Auto-captured findings\n\n")
	b.WriteString(fmt.Sprintf("Question: %s\n\n", brief.Objective))
	b.WriteString("## Inspected paths\n\n")
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Summary) != "" {
			b.WriteString(fmt.Sprintf("- %s\n", result.Summary))
		}
	}
	return writeNoteToolFile(workspaceRoot, "auto-findings", b.String())
}

func validateNoteToolKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("note key is required")
	}
	if len(key) > noteToolMaxKeyLength {
		return "", fmt.Errorf("note key is too long (max %d bytes)", noteToolMaxKeyLength)
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, `/\`) || filepath.IsAbs(key) {
		return "", fmt.Errorf("note key must be a slug, not a path")
	}
	if !noteToolKeyPattern.MatchString(key) {
		return "", fmt.Errorf("note key must start with an ASCII letter or digit and contain only ASCII letters, digits, '.', '_' or '-'")
	}
	return key, nil
}

func validateNoteToolContent(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("note content is required")
	}
	if len([]byte(raw)) > noteToolMaxContentBytes {
		return "", fmt.Errorf("note content is too large (max %d bytes)", noteToolMaxContentBytes)
	}
	return raw, nil
}

func writeNoteToolFile(workspaceRoot string, key string, content string) (string, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", fmt.Errorf("workspace root is required for note writes")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	idiomDir := filepath.Join(absRoot, ".idiom")
	if err := ensureNoteToolDirectory(idiomDir, 0o700, ".idiom"); err != nil {
		return "", err
	}
	noteDir := filepath.Join(idiomDir, "notes")
	if err := ensureNoteToolDirectory(noteDir, 0o700, ".idiom/notes"); err != nil {
		return "", err
	}
	target := filepath.Join(noteDir, key+".md")
	if rel, err := filepath.Rel(noteDir, target); err != nil || rel != key+".md" {
		return "", fmt.Errorf("note path escaped notes directory")
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("note target is a symlink: %s", filepath.ToSlash(filepath.Join(".idiom", "notes", key+".md")))
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	tmp, err := os.CreateTemp(noteDir, "."+key+"-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return "", err
	}
	cleanup = false
	return filepath.ToSlash(filepath.Join(".idiom", "notes", key+".md")), nil
}

func ensureNoteToolDirectory(path string, perm os.FileMode, label string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; note writes require a real directory", label)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s exists but is not a directory", label)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(path, perm); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s was not created as a real directory", label)
	}
	return nil
}
