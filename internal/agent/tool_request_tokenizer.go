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

import "idiom/internal/workspace"

// ToolRequestTokenizer converts raw model output into a tool name and
// key-value field map. Returns ok=false when the input does not match
// this tokenizer's format.
type ToolRequestTokenizer interface {
	Tokenize(raw string) (tool string, fields map[string]string, ok bool)
}

// ParseResult is the unified output of all tokenizers after normalization.
type ParseResult struct {
	Request workspace.ToolRequest
	Raw     string
	OK      bool
}

// ParseToolRequestFromTokenizer runs a tokenizer and converts its output
// through the shared toolRequestFromSyntaxValues function. This is the
// single convergence point for all parsing strategies.
func ParseToolRequestFromTokenizer(t ToolRequestTokenizer, raw string) ParseResult {
	tool, fields, ok := t.Tokenize(raw)
	if !ok {
		return ParseResult{}
	}
	req, ok := toolRequestFromSyntaxValues(tool, fields)
	if !ok {
		return ParseResult{}
	}
	return ParseResult{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		OK:      true,
	}
}
