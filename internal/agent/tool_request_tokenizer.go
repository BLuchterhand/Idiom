// Package agent: ToolRequestTokenizer provides a unified interface for
// parsing raw model output into (tool, fields) pairs. Each backend-specific
// format (key-value, function-call, fenced JSON, CLI, nested args)
// implements one Tokenizer. The shared toolRequestFromSyntaxValues
// function converts the (tool, fields) map into a ToolRequest.
//
// Previously each parser had its own bespoke string-walking logic and
// error classification. This interface consolidates tokenization so
// adding a new format or field requires one new Tokenizer implementation
// and one change to toolRequestFromSyntaxValues — not N parallel edits.

package agent

import (
	"strings"

	"idiom/internal/workspace"
)

// ToolRequestTokenizer converts raw model output into a tool name and
// key-value field map.
type ToolRequestTokenizer interface {
	Tokenize(raw string) ToolRequestTokenization
}

type ToolRequestTokenization struct {
	Tool   string
	Fields map[string]string
	OK     bool
	Unsafe bool
}

// ParseResult is the unified output of all tokenizers after normalization.
type ParseResult struct {
	Request workspace.ToolRequest
	Raw     string
	OK      bool
	Unsafe  bool
}

// ParseToolRequestFromTokenizer runs a tokenizer and converts its output
// through the shared toolRequestFromSyntaxValues function. This is the
// single convergence point for all parsing strategies.
func ParseToolRequestFromTokenizer(t ToolRequestTokenizer, raw string) ParseResult {
	tokens := t.Tokenize(raw)
	if tokens.Unsafe {
		return ParseResult{Unsafe: true}
	}
	if !tokens.OK {
		return ParseResult{}
	}
	req, ok := toolRequestFromSyntaxValues(tokens.Tool, tokens.Fields)
	if !ok {
		return ParseResult{Unsafe: true}
	}
	return ParseResult{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		OK:      true,
	}
}

type keyValueToolRequestTokenizer struct{}

func (keyValueToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ToolRequestTokenization{}
	}
	lines := strings.Split(trimmed, "\n")
	toolLineIdx := -1
	proseLines := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "tool") {
			toolLineIdx = i
			break
		}
		proseLines++
		if proseLines > 5 {
			return ToolRequestTokenization{Unsafe: true}
		}
	}
	if toolLineIdx < 0 {
		if keyValueNoToolUnsafe(lines) {
			return ToolRequestTokenization{Unsafe: true}
		}
		return ToolRequestTokenization{}
	}

	values := map[string]string{}
	sawKeyValue := false
	lastKey := ""
	continuationLines := 0
	appendContinuation := func(line string) bool {
		if !keyValueContinuationField(lastKey) {
			return true
		}
		continuationLines++
		if continuationLines > 8 || len(values[lastKey])+len(line)+1 > 8192 {
			return false
		}
		values[lastKey] += "\n" + line
		return true
	}
	for _, rawLine := range lines[toolLineIdx:] {
		line := strings.TrimRight(rawLine, "\r")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			if sawKeyValue && keyValueBlankContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}
				continue
			}
			if sawKeyValue && strings.TrimSpace(values["tool"]) != "" {
				break
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			if sawKeyValue {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}
				continue
			}
			return ToolRequestTokenization{Unsafe: true}
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if sawKeyValue && keyValueRawValueField(lastKey) && !keyValueRawFieldStructuralTransition(lastKey, key) {
			if !appendContinuation(line) {
				return ToolRequestTokenization{Unsafe: true}
			}
			continue
		}
		if keyValueRawValueField(key) {
			value = normalizeRawKeyValueField(value)
		} else {
			value = trimMatchingSyntaxQuotes(strings.TrimSpace(value))
		}
		if key == "" || (value == "" && !keyValueAllowsEmptyValue(key, values)) {
			return ToolRequestTokenization{Unsafe: true}
		}
		if key == "tool" && strings.TrimSpace(values["tool"]) != "" {
			break
		}
		if !keyValueToolRequestFieldAllowed(key) {
			if sawKeyValue && keyValueContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}
				continue
			}
			return ToolRequestTokenization{Unsafe: true}
		}
		sawKeyValue = true
		continuationLines = 0
		values[key] = value
		lastKey = key
	}
	if !sawKeyValue {
		return ToolRequestTokenization{Unsafe: true}
	}
	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return ToolRequestTokenization{Unsafe: true}
	}
	return ToolRequestTokenization{Tool: tool, Fields: values, OK: true}
}

func keyValueNoToolUnsafe(lines []string) bool {
	for _, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if keyValueMissingToolActionField(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}
	return false
}

func keyValueMissingToolActionField(key string) bool {
	switch key {
	case "path", "file", "scope", "pattern", "query", "symbol", "capability", "operation", "action", "kind",
		"mode", "task", "glob", "language", "resolution", "named_gap", "why_not_answering", "start_line",
		"end_line", "limit", "budget_tokens", "max_depth", "include_evidence", "include_source", "include_files",
		"refresh", "lines", "replace_all", "purpose", "command", "content", "prompt", "agent", "workdir",
		"handle", "root", "destination", "packet_path", "output_path", "component", "category", "session_id",
		"task_id", "recursive", "old_text", "new_text", "new_text_ref", "content_ref":
		return true
	default:
		return false
	}
}

func normalizeRawKeyValueField(value string) string {
	if strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") {
		value = value[1:]
	}
	return trimMatchingSyntaxQuotes(value)
}

func trimMatchingSyntaxQuotes(value string) string {
	if len(value) < 2 {
		return value
	}
	first := value[0]
	last := value[len(value)-1]
	if (first == '"' || first == '\'') && first == last {
		return value[1 : len(value)-1]
	}
	return value
}

func keyValueRawValueField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueBlankContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueRawFieldStructuralTransition(lastKey, key string) bool {
	switch strings.ToLower(strings.TrimSpace(lastKey)) {
	case "old_text":
		return key == "new_text"
	case "new_text":
		switch key {
		case "description", "task_id", "replace_all":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func keyValueAllowsEmptyValue(key string, values map[string]string) bool {
	return key == "new_text" && strings.TrimSpace(values["tool"]) == "edit_file"
}

func keyValueContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "prompt", "content", "old_text", "new_text", "description", "purpose", "task", "query", "named_gap", "why_not_answering", "expected", "observed", "workaround", "impact", "suggested_fix":
		return true
	default:
		return false
	}
}
