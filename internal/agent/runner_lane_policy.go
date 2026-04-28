package agent

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"idiom/internal/workspace"
)

type runnerLanePreflightInput struct {
	Request               workspace.ToolRequest
	Raw                   string
	Results               []workspace.ToolResult
	VerificationAttempted bool
}

type runnerLanePreflightDecision struct {
	OriginalRequest  workspace.ToolRequest
	OriginalRaw      string
	Request          workspace.ToolRequest
	Raw              string
	RejectSummary    string
	RejectError      string
	RejectStopReason string
	ForceAnswer      *runnerForcedAnswer
}

type runnerForcedAnswer struct {
	Reason string
	Prompt string
}

// applyLanePreflight is the simplified, OpenCode-shaped tool gate.
//
// History: this function used to host ~120 lines of redirects across
// delegated_read_only bootstrap, secondary-evidence redirection, named
// file read redirection, search-hit window redirection, integration
// pivots, post-readiness symbol body upgrades, premature target reads,
// evidence-first read budgets, write-phase enforcement, debug-log phase
// scope, debug-log followups, and post-evidence drift. They were Qwen
// handholding written to compensate for the prompt not telling the model
// what to do; once prompts/opencode/qwen.md made discovery first-class
// the redirects became the wrong shape.
//
// What remains is the OpenCode-aligned set: the two intent-based
// contracts a user prompt can request (conversation-only and
// verification-only) plus pass-through. Everything else is the model's
// job, shaped by the system prompt.
func (r *Runner) applyLanePreflight(input runnerLanePreflightInput) runnerLanePreflightDecision {
	decision := runnerLanePreflightDecision{
		OriginalRequest: input.Request,
		OriginalRaw:     input.Raw,
		Request:         input.Request,
		Raw:             input.Raw,
	}

	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "conversation_only") {
		decision.RejectSummary = "Tool request rejected by conversation-only contract"
		decision.RejectError = "this turn is conversation-only. Do not call any tools; answer directly from the prompt and prior visible context."
		decision.RejectStopReason = "conversation_only_tool_rejected"
		return decision
	}

	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "read_only_verification_shell") &&
		!isVerificationRequest(decision.Request) {
		if !input.VerificationAttempted {
			if redirected, ok := r.verificationBootstrapRequest(); ok {
				decision.Request = redirected
				decision.Raw = marshalToolRequestRaw(redirected)
			} else {
				decision.RejectSummary = "Verification-first contract rejected non-verification action"
				decision.RejectError = "this turn is verification-only. Run the requested verification command first with shell (for example go test ./..., go build ./..., npm test, or the seeded failing command), then inspect files only if the output needs explanation."
				decision.RejectStopReason = "verification_first_contract_exhausted"
				return decision
			}
		} else {
			decision.RejectSummary = "Verification-only contract rejected post-verification drift"
			decision.RejectError = "this turn is verification-only and a verification command has already run. Report the pass/fail result directly unless another verification command is strictly necessary."
			decision.RejectStopReason = "verification_only_post_check_drift"
			return decision
		}
	}

	return decision
}

// delegatedReadOnlyAnswerReadyAfterResult reports whether a delegated
// read-only child task has gathered enough evidence to answer. The
// runner uses this to short-circuit additional tool calls when a
// composite discovery packet has landed or when at least two
// implementation files have been inspected.
func (r *Runner) delegatedReadOnlyAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}
	if !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return false
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}
	tool := strings.TrimSpace(firstNonEmpty(result.Tool, req.Tool))
	switch {
	case tool == "discovery_query":
		if !delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
			return false
		}
	case isReadClassTool(workspace.ToolRequest{Tool: tool}):
		if len(delegatedReadOnlyInspectedImplementationPaths(results)) < 2 {
			return false
		}
	default:
		return false
	}
	return true
}

// readOnlyAnalysisAnswerReadyAfterResult reports whether a top-level
// read-only analysis turn (architecture / review contracts) has enough
// evidence to answer. Mirrors the delegated path but applies to the
// main agent rather than a child task.
func (r *Runner) readOnlyAnalysisAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	_ = req
	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}
	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return false
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}
	switch strings.TrimSpace(r.RuntimeBrief.Contract) {
	case "architecture_assessment", "review_findings":
	default:
		return false
	}
	exactProbes := readOnlyAnalysisExactProbeCount(results)
	evidencePaths := promptEvidencePathsFromToolResults(results, 6)
	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		return exactProbes >= 1 && len(evidencePaths) >= 2
	}
	return exactProbes >= 2 && len(evidencePaths) >= 1
}

func readOnlyAnalysisExactProbeCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "read_file":
			if span := toolResultReadSpan(result); span > 0 && span <= 120 {
				count++
			}
		case "go_read_symbol", "go_extract_body",
			"go_calls_from", "go_calls_into", "go_references", "go_definition", "go_workspace_symbol",
			"cpp_read_symbol", "cpp_extract_body",
			"cpp_calls_from", "cpp_calls_into", "cpp_references", "cpp_definition", "cpp_workspace_symbol",
			"python_read_symbol", "python_calls_from", "python_calls_into", "python_references", "python_definition", "python_workspace_symbol",
			"ts_read_symbol", "ts_calls_from", "ts_calls_into", "ts_references", "ts_definition", "ts_workspace_symbol",
			"rust_read_symbol", "rust_calls_from", "rust_calls_into", "rust_references", "rust_definition", "rust_workspace_symbol",
			"java_read_symbol", "java_calls_from", "java_calls_into", "java_references", "java_definition", "java_workspace_symbol":
			count++
		}
	}
	return count
}

func toolResultReadSpan(result workspace.ToolResult) int {
	match := regexp.MustCompile(`lines (\d+)-(\d+)`).FindStringSubmatch(result.Summary)
	if len(match) != 3 {
		return 0
	}
	start, errStart := strconv.Atoi(match[1])
	end, errEnd := strconv.Atoi(match[2])
	if errStart != nil || errEnd != nil || end < start {
		return 0
	}
	return end - start + 1
}

func delegatedReadOnlyHasCompositeDiscoveryPacket(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "discovery_query" &&
			strings.TrimSpace(result.Error) == "" &&
			strings.Contains(result.Output, `"artifact_path"`) &&
			strings.Contains(result.Output, `"evidence"`) {
			return true
		}
	}
	return false
}

// delegatedReadOnlyLiteralSymbolCandidate decides whether a token from
// the prompt looks like a code symbol worth pursuing as an exact probe.
// Used by the runner / final answer decorator to anchor evidence on
// named identifiers rather than free-text guesses.
func delegatedReadOnlyLiteralSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))
	if value == "" {
		return "", false
	}
	if len(value) < 4 {
		return "", false
	}
	hasLetter := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r), r == '_':
		default:
			return "", false
		}
	}
	if !hasLetter {
		return "", false
	}
	if !delegatedReadOnlyLooksCodeShapedSymbol(value) {
		return "", false
	}
	lower := strings.ToLower(value)
	switch lower {
	case "agent", "codebase", "architecture", "configuration", "config", "configs", "prompt", "prompts", "main", "files", "file", "directory", "directories", "entry", "entrypoints", "line", "lines", "harness", "implementation", "source", "truth", "before", "tests", "test", "readme", "docs", "start", "subagent", "subagents", "delegate", "delegated", "task", "runner", "package", "import", "const", "type", "func", "return", "use", "uses", "using", "utilize", "utilizes", "utilise", "utilises", "make", "makes":
		return "", false
	}
	return value, true
}

func delegatedReadOnlyLooksCodeShapedSymbol(value string) bool {
	if strings.Contains(value, "_") {
		return true
	}
	hasLower := false
	for i, r := range value {
		switch {
		case unicode.IsDigit(r):
			return true
		case unicode.IsLower(r):
			hasLower = true
		case i > 0 && unicode.IsUpper(r) && hasLower:
			return true
		}
	}
	return false
}

// appendUniqueSearchTerm appends value to values if not already present.
// Used by the answer-composer / final-answer pipeline when building
// search-pattern term lists.
func appendUniqueSearchTerm(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func delegatedReadOnlyInspectedImplementationPaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "read_file",
			"go_read_symbol", "go_file_outline", "go_extract_body",
			"cpp_read_symbol", "cpp_file_outline", "cpp_extract_body",
			"python_read_symbol", "python_file_outline",
			"ts_read_symbol", "ts_file_outline",
			"rust_read_symbol", "rust_file_outline",
			"java_read_symbol", "java_file_outline":
		default:
			continue
		}
		for _, path := range pinnedPathsFromToolResults([]workspace.ToolResult{result}, 4) {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}
