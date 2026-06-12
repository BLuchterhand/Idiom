package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/scratchnotes"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const (
	noteToolName            = "note"
	noteToolMaxKeyLength    = scratchnotes.MaxKeyLength
	noteToolMaxContentBytes = scratchnotes.MaxContentBytes
)

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
	summaries := make([]string, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Summary) != "" {
			summaries = append(summaries, result.Summary)
		}
	}

	return scratchnotes.WriteAutoFindings(workspaceRoot, brief.Objective, summaries)
}

func validateNoteToolKey(raw string) (string, error) {
	return scratchnotes.ValidateKey(raw)
}

func validateNoteToolContent(raw string) (string, error) {
	return scratchnotes.ValidateContent(raw)
}

func writeNoteToolFile(workspaceRoot string, key string, content string) (string, error) {
	return scratchnotes.WriteFile(workspaceRoot, key, content)
}
