package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/workerruntime"
)

func readOnlyInvestigationStrategyPrompt() string {
	return "## Investigation Strategy\n\n" +
		"For read-only investigation questions, an efficient approach:\n\n" +
		"1. Broad orientation (1 call): Start with context_pack or context_query for a ranked map of relevant files and symbols. One call replaces 20+ individual symbol lookups.\n" +
		"2. Bundled probe (1 call): Use discovery_query with purpose=\"architecture_trace\" or \"impact_analysis\" to orchestrate multiple probes at once. One call replaces 10+ separate searches.\n" +
		"3. Targeted reads (2-3 calls): Read only the 1-3 key files or symbols identified above. Do not enumerate every symbol in a file.\n" +
		"4. Answer: Once the evidence answers the question, stop. You do not need to exhaustively verify every related function.\n\n" +
		"If you are reading a file symbols one by one, pause and use context_query or discovery_query instead."
}

func narrowToolFamilyRepetitionNudgePrompt(narrowFamily string, consecutiveCalls int) string {
	return fmt.Sprintf(
		"You have called %s %d times consecutively. If you still lack the evidence to answer, switch strategy: use `context_query` for a graph-shaped question, `discovery_query` for a bundled layered probe, `harness_audit` for harness self-inspection, or re-read the initial context pack. Repeated narrow symbol lookups rarely uncover the high-level answer faster than one broader probe.",
		narrowFamily, consecutiveCalls,
	)
}

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

type delegatedDiscoveryPlan struct {
	objective       string
	namedScopes     []string
	hasNamedScope   bool
	hasPrimaryScope bool
	primaryScope    string
	detailedScopes  []string
	defaultPurpose  string
	searchPattern   string
	symbolPattern   string
	questionText    string
	promptKeyTerms  []string
}

func newDelegatedDiscoveryPlan(objective string) delegatedDiscoveryPlan {
	objective = strings.TrimSpace(objective)

	plan := delegatedDiscoveryPlan{objective: objective}

	plan.namedScopes = plan.parseNamedScopes()
	plan.hasNamedScope = len(plan.namedScopes) > 0
	plan.hasPrimaryScope = plan.computeHasPrimaryNamedScope()
	plan.primaryScope = plan.computePrimaryScope()
	plan.detailedScopes = plan.computeDetailedNamedScopes()
	plan.questionText = plan.computeQuestionText()
	plan.defaultPurpose = plan.computePurpose()
	plan.searchPattern = plan.computeSearchPattern()
	plan.symbolPattern = plan.computeSymbolPattern()
	plan.promptKeyTerms = plan.computePromptKeyTerms()

	return plan
}

func (p delegatedDiscoveryPlan) repairDiscoveryQuery(req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	repaired := req
	changed := false

	if purpose := p.normalizePurpose(repaired.Purpose); purpose != strings.TrimSpace(repaired.Purpose) {
		repaired.Purpose = purpose
		changed = true
	}

	if p.shouldReplacePath(repaired.Path) {
		repaired.Path = p.primaryScope
		changed = true
	}

	if strings.TrimSpace(repaired.Pattern) == "" {
		repaired.Pattern = p.searchPattern
		changed = true
	}

	if repaired.MaxDepth <= 0 {
		repaired.MaxDepth = 2
		changed = true
	}

	return repaired, changed
}

func (p delegatedDiscoveryPlan) candidateConsumptionRequest(results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, bool) {
	if !containsRunnerTool(allowedTools, "read_file") || len(delegatedReadOnlySourceBodyEvidencePaths(results)) > 0 {
		return workspace.ToolRequest{}, false
	}

	paths := p.consumableSourceBodyCandidatePaths(results, 6)
	for _, path := range paths {
		path = normalizeRunnerPath(path)
		if path == "" || concreteSourceEvidenceReadForPath(path, results) {
			continue
		}

		return workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         160,
			NamedGap:        "source-body evidence for delegated read-only synthesis",
			WhyNotAnswering: "the prior discovery query returned candidates but not source-body evidence",
		}, true
	}

	return workspace.ToolRequest{}, false
}

type delegatedDiscoveryConsumableCandidate struct {
	path       string
	score      int
	firstIndex int
}

func (p delegatedDiscoveryPlan) consumableSourceBodyCandidatePaths(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := p.consumableCandidateTerms()
	best := map[string]delegatedDiscoveryConsumableCandidate{}
	seenIndex := 0

	for _, summary := range delegatedDiscoverySummaries(results) {
		for _, evidence := range summary.Evidence {
			layerName := strings.TrimSpace(evidence.Name)
			summaryText := strings.TrimSpace(evidence.Summary)
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))

			for _, rawLine := range strings.Split(text, "\n") {
				line := strings.TrimSpace(rawLine)
				if line == "" {
					continue
				}

				for _, path := range delegatedDiscoveryLineCandidatePaths(line) {
					path = normalizeDelegatedDiscoveryEvidencePath(path)
					if !delegatedDiscoveryConsumableSourcePath(path) {
						continue
					}

					score := p.consumableCandidateScore(path, layerName, line, summaryText, terms)
					if score <= 0 {
						continue
					}

					candidate := delegatedDiscoveryConsumableCandidate{path: path, score: score, firstIndex: seenIndex}
					seenIndex++

					if prior, ok := best[path]; ok && (prior.score > score || (prior.score == score && prior.firstIndex <= candidate.firstIndex)) {
						continue
					}

					best[path] = candidate
				}
			}
		}
	}

	candidates := make([]delegatedDiscoveryConsumableCandidate, 0, len(best))
	for _, candidate := range best {
		candidates = append(candidates, candidate)
	}

	if len(candidates) == 0 {
		return nil
	}

	sortDelegatedDiscoveryConsumableCandidates(candidates)

	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}

	return closurePromptEvidencePaths(paths, max)
}

func delegatedDiscoveryLineCandidatePaths(line string) []string {
	var paths []string

	for _, path := range pathparse.MentionedWorkspacePaths(line) {
		paths = util.AppendUnique(paths, path)
	}

	for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
		paths = util.AppendUnique(paths, match)
	}

	return paths
}

func delegatedDiscoveryConsumableSourcePath(path string) bool {
	path = normalizeDelegatedDiscoveryEvidencePath(path)
	if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
		return false
	}

	if !delegatedDiscoveryLooksLikeEvidencePath(path) {
		return false
	}

	if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
		return false
	}

	return true
}

func (p delegatedDiscoveryPlan) consumableCandidateTerms() []string {
	var terms []string

	for _, term := range agentutil.RecoveryObjectiveSearchWords(p.questionText, 16) {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	for _, term := range p.promptKeyTerms {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	for _, term := range delegatedReadOnlyMechanismTerms(p.objective) {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	return terms
}

func appendDelegatedDiscoveryCandidateTerm(terms []string, raw string) []string {
	term := strings.ToLower(strings.TrimSpace(raw))

	term = strings.Trim(term, "`'\"()[]{}<>,.:;!?")

	if term == "" || len(term) < 4 || pathparse.LooksLikeWorkspacePathToken(term) {
		return terms
	}

	if delegatedDiscoveryStopWord(term, delegatedDiscoveryStopWordSearchTerm) ||
		delegatedDiscoveryStopWord(term, delegatedDiscoveryStopWordPromptKeyTerm) {

		return terms
	}

	return util.AppendUnique(terms, term)
}

func (p delegatedDiscoveryPlan) consumableCandidateScore(path string, layerName string, line string, summary string, terms []string) int {
	context := strings.TrimSpace(summary + "\n" + line)
	score := delegatedDiscoveryConsumableLayerScore(layerName)

	score += delegatedDiscoveryConsumableLineScore(line)
	score += delegatedDiscoveryConsumablePathShapeScore(path)
	score += delegatedDiscoveryConsumableObjectiveScore(path, context, terms)
	score += delegatedDiscoveryConsumableMechanismScore(path, layerName, context)
	score += delegatedDiscoveryConsumableInventoryScore(layerName, context)

	if p.pathOutsideDetailedScope(path) {
		score -= 80
	}

	return score
}

func delegatedDiscoveryConsumableLayerScore(name string) int {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return 80
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return 75
	case "go_references", "cpp_references", "python_references", "ts_references", "rust_references", "java_references":
		return 65
	case "go_definition", "cpp_definition", "python_definition", "ts_definition", "rust_definition", "java_definition":
		return 60
	case "go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return 45
	case "related_files":
		return 10
	case "dependency_graph", "ctags_symbols":
		return 5
	case "target_listing":
		return -45
	case "project_behavior_digest", "project_context_digest":
		return -60
	default:
		return 5
	}
}

func delegatedDiscoveryConsumableLineScore(line string) int {
	line = strings.TrimSpace(line)

	lower := strings.ToLower(line)

	switch {
	case line == "":
		return -20
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 45
	case strings.Contains(lower, "implementation anchor"), strings.Contains(lower, "implementation path"), strings.Contains(lower, "source anchor"):
		return 40
	case strings.Contains(lower, "func "):
		return 35
	case strings.Contains(lower, "type "):
		return 20
	case strings.Contains(lower, "package "):
		return -25
	case delegatedDiscoveryLineIsBarePath(line):
		return -20
	default:
		return 0
	}
}

func delegatedDiscoveryConsumablePathShapeScore(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 25
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 20
	case strings.HasPrefix(path, "internal/"):
		return 10
	case strings.HasSuffix(path, "_test.go"):
		return -30
	default:
		return 0
	}
}

func delegatedDiscoveryConsumableObjectiveScore(path string, line string, terms []string) int {
	haystack := strings.ToLower(filepath.ToSlash(path) + "\n" + line)
	score := 0

	for _, term := range terms {
		if term == "" {
			continue
		}

		if strings.Contains(haystack, term) {
			score += 22
		}
	}

	return score
}

func delegatedDiscoveryConsumableMechanismScore(path string, layerName string, line string) int {
	if delegatedDiscoveryConsumableInventoryLayer(layerName) {
		return 0
	}

	haystack := strings.ToLower(filepath.ToSlash(path) + "\n" + line)
	score := 0

	if containsAny(haystack,
		"compose", "composer", "composition", "synthesis", "synthesize", "final response", "final answer",
		"closeout", "readiness", "completion", "acceptance", "grounded evidence", "mechanism", "contract",
	) {

		score += 55
	}

	if containsAny(haystack, "implementation path", "implementation anchor", "source anchor", "source-body", "source body") {
		score += 25
	}

	if containsAny(haystack, "capability:", "language:", "symbol index:", "related files:", "found related") {
		score -= 35
	}

	return score
}

func delegatedDiscoveryConsumableInventoryScore(layerName string, line string) int {
	lowerLayer := strings.ToLower(strings.TrimSpace(layerName))
	lower := strings.ToLower(strings.TrimSpace(line))
	score := 0

	if delegatedDiscoveryConsumableInventoryLayer(lowerLayer) {
		score -= 20
	}

	if containsAny(lower,
		"no go workspace symbols matched",
		"no workspace symbols matched",
		"no symbols matched",
		"no related go files matched",
		"found related go file",
		"found related go files",
		"capability:",
		"language:",
		"symbol index:",
		"related files:",
	) {

		score -= 45
	}

	return score
}

func delegatedDiscoveryConsumableInventoryLayer(layerName string) bool {
	lowerLayer := strings.ToLower(strings.TrimSpace(layerName))

	return lowerLayer == "target_listing" ||
		lowerLayer == "related_files" ||
		lowerLayer == "dependency_graph" ||
		lowerLayer == "ctags_symbols" ||
		strings.Contains(lowerLayer, "workspace_symbol")
}

func sortDelegatedDiscoveryConsumableCandidates(candidates []delegatedDiscoveryConsumableCandidate) {
	for i := 1; i < len(candidates); i++ {
		current := candidates[i]
		j := i - 1

		for ; j >= 0 && delegatedDiscoveryConsumableLess(current, candidates[j]); j-- {
			candidates[j+1] = candidates[j]
		}

		candidates[j+1] = current
	}
}

func delegatedDiscoveryConsumableLess(left, right delegatedDiscoveryConsumableCandidate) bool {
	if left.score != right.score {
		return left.score > right.score
	}

	leftPriority := delegatedDiscoveryPathPriority(left.path)
	rightPriority := delegatedDiscoveryPathPriority(right.path)

	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}

	if left.firstIndex != right.firstIndex {
		return left.firstIndex < right.firstIndex
	}

	return left.path < right.path
}

func (p delegatedDiscoveryPlan) normalizePurpose(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if delegatedDiscoveryPurposeValid(purpose) && p.purposeSupportedByObjective(purpose) {
		return purpose
	}

	return p.defaultPurpose
}

func (p delegatedDiscoveryPlan) purposeSupportedByObjective(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case "security_review":
		lower := strings.ToLower(p.objective)
		return containsAny(lower, "security", "unsafe", "vulnerability", "injection", "secret", "token")
	default:
		return true
	}
}

func (p delegatedDiscoveryPlan) shouldReplacePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}

	if path == "." && p.primaryScope != "" && p.primaryScope != "." && p.hasNamedScope {
		return true
	}

	if delegatedReadOnlyBroadNamedScope(path) && p.primaryScope != "" && p.primaryScope != "." && normalizeRunnerPath(path) != normalizeRunnerPath(p.primaryScope) {
		return true
	}

	return p.pathOutsideDetailedScope(path) && p.primaryScope != "" && p.primaryScope != "."
}

func (p delegatedDiscoveryPlan) pathOutsideDetailedScope(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || path == "." || len(p.detailedScopes) == 0 {
		return false
	}

	for _, scope := range p.detailedScopes {
		if postChildPathsOverlap(path, scope) {
			return false
		}
	}

	return true
}

func (p delegatedDiscoveryPlan) parseNamedScopes() []string {
	scopes := make([]string, 0, 4)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "named workspace scope:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Named workspace scope:"):], ",") {
			candidate = strings.TrimSpace(strings.Trim(candidate, "`'\""))
			if candidate == "" {
				continue
			}

			candidate = filepath.ToSlash(strings.TrimSuffix(strings.TrimPrefix(candidate, "./"), "/"))
			scopes = util.AppendUnique(scopes, candidate)
		}
	}

	return scopes
}

func (p delegatedDiscoveryPlan) computeHasPrimaryNamedScope() bool {
	for _, scope := range p.namedScopes {
		if !delegatedReadOnlySecondaryEvidencePath(scope) {
			return true
		}
	}

	return false
}

func (p delegatedDiscoveryPlan) computePrimaryScope() string {
	if delegatedReadOnlyChildMechanismQuestion(p.objective) && !delegatedObjectiveNeedsEntrypointScope(p.objective) {
		return "internal/runtime/agent"
	}

	firstCandidate := ""

	for _, candidate := range p.namedScopes {
		if candidate == "" {
			continue
		}

		if firstCandidate == "" {
			firstCandidate = candidate
		}

		if !delegatedReadOnlySecondaryEvidencePath(candidate) {
			return candidate
		}
	}

	if firstCandidate != "" {
		return firstCandidate
	}

	for _, candidate := range pathparse.MentionedWorkspacePaths(p.objective) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		if pathparse.HasWorkspaceFileExtension(candidate) {
			dir := filepath.ToSlash(filepath.Dir(candidate))
			if dir != "." && dir != "" {
				return dir
			}

			return candidate
		}

		return filepath.ToSlash(candidate)
	}

	return "."
}

func (p delegatedDiscoveryPlan) computeDetailedNamedScopes() []string {
	var scopes []string

	for _, candidate := range p.namedScopes {
		candidate = normalizeRunnerPath(strings.Trim(strings.TrimSpace(candidate), "`'\""))
		if candidate == "" || candidate == "." || delegatedReadOnlyBroadNamedScope(candidate) || delegatedReadOnlySecondaryEvidencePath(candidate) {
			continue
		}

		scopes = mergeRunnerStateStrings(scopes, []string{candidate})
	}

	return scopes
}

func (p delegatedDiscoveryPlan) computePurpose() string {
	lower := strings.ToLower(strings.TrimSpace(p.questionText))
	if lower == "" {
		lower = strings.ToLower(p.computeQuestionText())
	}

	switch {
	case containsAny(lower, "security", "unsafe", "vulnerability", "injection", "secret", "token"):
		return "security_review"
	case containsAny(lower, "git history", "history of", "historical", "evolution", "changed over time", "regression introduced"):
		return "history_context"
	case containsAny(lower, "call", "calls", "trace", "tracing", "invoked", "invoke", "entrypoint"):
		return "function_tracing"
	case containsAny(lower, "impact", "blast radius", "affected", "change"):
		return "impact_analysis"
	default:
		return "architecture_boundary"
	}
}

func (p delegatedDiscoveryPlan) computeQuestionText() string {
	lines := make([]string, 0, 2)

	for _, raw := range strings.Split(p.objective, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return ") ||
			strings.HasPrefix(lower, "- ") {

			continue
		}

		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return p.objective
	}

	return strings.Join(lines, " ")
}

func (p delegatedDiscoveryPlan) computeSearchPattern() string {
	terms := make([]string, 0, 6)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)

		lower := strings.ToLower(line)

		if !strings.HasPrefix(lower, "key terms:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Key terms:"):], ",") {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = util.AppendUnique(terms, term)
			}
		}
	}

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = util.AppendUnique(terms, term)
		}
	}

	if len(terms) == 0 {
		for _, candidate := range strings.Fields(p.questionText) {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = util.AppendUnique(terms, term)
			}

			if len(terms) >= 4 {
				break
			}
		}
	}

	for _, term := range delegatedReadOnlyMechanismTerms(p.objective) {
		if normalized, ok := delegatedReadOnlySearchTerm(term); ok {
			terms = util.AppendUnique(terms, normalized)
		}
	}

	return strings.Join(terms, "|")
}

func (p delegatedDiscoveryPlan) computeSymbolPattern() string {
	candidates := make([]string, 0, 6)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)

		lower := strings.ToLower(line)

		if !strings.HasPrefix(lower, "key terms:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Key terms:"):], ",") {
			if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(candidate); ok {
				candidates = util.AppendUnique(candidates, symbol)
			}
		}
	}

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(fragment); ok {
			candidates = util.AppendUnique(candidates, symbol)
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	return candidates[0]
}

func (p delegatedDiscoveryPlan) computePromptKeyTerms() []string {
	terms := make([]string, 0, 6)

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" || pathparse.LooksLikeWorkspacePathToken(fragment) {
			continue
		}

		lower := strings.ToLower(fragment)
		if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordPromptKeyTerm) {
			continue
		}

		terms = util.AppendUnique(terms, fragment)
	}

	for _, candidate := range strings.Fields(p.objective) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || pathparse.LooksLikeWorkspacePathToken(candidate) {
			continue
		}

		lower := strings.ToLower(strings.Trim(candidate, "`'\"()[]{}<>,.:;!?"))
		if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordPromptKeyTerm) {
			continue
		}

		if len(lower) < 5 {
			continue
		}

		terms = util.AppendUnique(terms, lower)
		if len(terms) >= 8 {
			break
		}
	}

	return terms
}

type delegatedDiscoveryStopWordUse int

const (
	delegatedDiscoveryStopWordSearchTerm delegatedDiscoveryStopWordUse = iota
	delegatedDiscoveryStopWordPromptKeyTerm
	delegatedDiscoveryStopWordSymbol
)

func delegatedDiscoveryStopWord(lower string, use delegatedDiscoveryStopWordUse) bool {
	switch strings.TrimSpace(lower) {
	case "",
		"agent", "codebase", "architecture", "configuration", "config", "configs", "prompt", "prompts", "main",
		"files", "file", "directory", "directories", "entry", "entrypoints", "line", "lines",
		"harness", "implementation", "source", "truth", "source-of-truth", "source_of_truth", "before", "tests", "test", "readme", "docs", "start",
		"internal", "workspace", "named", "scope", "question", "questions", "direct", "directly", "delegated", "repository", "relative",
		"evidence", "sufficient", "inspected", "guidance", "prefer", "targeted", "symbol", "symbols", "bounded", "read", "reads", "overview", "overviews",
		"continue", "readiness", "claim", "claims", "probe", "probes", "resolve", "materially", "incomplete":
		return true
	}

	if use == delegatedDiscoveryStopWordPromptKeyTerm {
		switch strings.TrimSpace(lower) {
		case "how", "does", "this", "explain", "inspect", "explore", "look", "overall", "return", "focus", "specifically", "emphasize",
			"use", "uses", "used", "using", "utilize", "utilizes", "utilized", "utilise", "utilises", "make", "makes", "made",
			"paths", "path", "findings", "finding", "confidence", "gap", "gaps", "key", "terms", "exact", "entrypoint", "agents":
			return true
		}
	}

	if use == delegatedDiscoveryStopWordSymbol {
		switch strings.TrimSpace(lower) {
		case "subagent", "subagents", "delegate", "task", "runner", "package", "import", "const", "type", "func", "return",
			"use", "uses", "using", "utilize", "utilizes", "utilise", "utilises", "make", "makes":
			return true
		}
	}

	return false
}

func (r *Runner) applyLanePreflight(input runnerLanePreflightInput) runnerLanePreflightDecision {
	decision := runnerLanePreflightDecision{
		OriginalRequest: input.Request,
		OriginalRaw:     input.Raw,
		Request:         input.Request,
		Raw:             input.Raw,
	}

	if workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		// Project-defined external tools are allowed even in conversation
		// mode — they're opaque shell invocations the host project
		// registered specifically so the agent could use them. Built-in
		// workspace tools (edit, shell, discovery_query, etc.) remain
		// rejected because conversation_only's contract is "no workspace
		// state change, no idiom-internal exploration." Externals
		// satisfy that contract by being host-controlled.
		toolName := strings.TrimSpace(decision.Request.Tool)
		isExternal := false

		if r.Workspace != nil {
			if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
				isExternal = extReg.Has(toolName)
			}
		}

		if !isExternal {
			decision.RejectSummary = "Tool request rejected by conversation-only contract"
			decision.RejectError = "this turn is conversation-only. Do not call built-in tools; project-defined external tools (registered via workspace.external_tools) are allowed if any are configured. Otherwise answer directly from the prompt and prior visible context."
			decision.RejectStopReason = "conversation_only_tool_rejected"

			return decision
		}
	}

	if redirected, ok := r.mainReadOnlyDelegationBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.mainReadOnlyBroadSearchContextPackRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyDiscoveryQueryRepairRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyPostDiscoveryInventoryRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyBootstrapQueryRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlySecondaryEvidenceRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyNamedFileReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlySearchHitReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyIntegrationPivotRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := postReadinessSymbolBodyUpgradeRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.planThenExecuteRequiredSourceFollowupRedirect(input.Results, decision.Request); ok {
		r.emitEvent(RunEvent{
			Role:    "plan_then_execute_source_followup_redirect",
			Content: "redirected a non-advancing required-path source read to the next bounded source-evidence step",
		})

		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) &&
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

	if summary, errorText, reject := r.conversationRepeatedBroadDiscoveryAfterGate(decision.Request, input.Results); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText

		return decision
	}

	if summary, errorText, reject := prematureTargetReadRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "premature_target_read_exhausted"

		return decision
	}

	if redirected, ok := evidenceFirstTargetReadRedirect(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.debugLogPreflightRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if summary, errorText, reject := debugLogPhaseScopeRejection(decision.Request, input.Results, r.RuntimeBrief, currentDebugLogSessionID(r.Workspace)); reject {
		if summary == "Blind log reread rejected" {
			decision.RejectSummary = summary
			decision.RejectError = errorText

			return decision
		}

		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "debug_logs_phase_scope_enforced"

		return decision
	}

	if redirected, ok := debugLogFollowupRequest(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if debugLogEvidenceSatisfied(r.RuntimeBrief, input.Results) && isReadClassTool(decision.Request) {
		decision.RejectSummary = "Debug-log contract rejected extra exploration after evidence was satisfied"
		decision.RejectError = "you already have a concrete failure hit from the selected log and a nearby read window. A final answer is available if that evidence supports it; otherwise name the specific missing datum before another probe."
		decision.RejectStopReason = "debug_logs_post_evidence_drift"

		return decision
	}

	return decision
}

func (r *Runner) conversationRepeatedBroadDiscoveryAfterGate(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, reject bool) {
	if r == nil || !workerruntime.TaskFamilyIsConversation(r.RuntimeBrief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) ||
		runnerBriefRequiresMutation(r.RuntimeBrief) ||
		runnerVerificationRequired(r.RuntimeBrief) {

		return "", "", false
	}

	if !conversationBroadLocalizationAfterCompletionGate(results) || !conversationBroadDiscoveryRequest(req) {
		return "", "", false
	}

	summary = "Conversation contract rejected repeated broad discovery after bounded evidence"

	// Check if the completion gate rejection was about source grounding
	// (the model cited files it never read). If so, call out the specific
	// missing files and tell the model to read them instead of searching.
	if missingPaths := conversationSourceGroundingMissingPaths(results); len(missingPaths) > 0 {
		errorText = "The completion gate rejected your answer because it cites file(s) that were never read: " +
			strings.Join(missingPaths, ", ") +
			". Use `read_file` on those file(s) instead of another broad search."
	} else {
		errorText = "the conversation lane already spent its recovery probe on broad localization after a completion-gate miss. Answer from the gathered evidence, or take one narrow read/search that names the concrete missing claim; do not repeat broad discovery."
	}

	return summary, errorText, true
}

// conversationSourceGroundingMissingPaths scans results for a completion_gate
// rejection caused by source_grounding_unmet and returns the paths cited in
// the error text that the model should read.
func conversationSourceGroundingMissingPaths(results []workspace.ToolResult) []string {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "completion_gate" {
			continue
		}

		errorText := strings.TrimSpace(result.Error)
		if errorText == "" {
			continue
		}

		// Detect source-grounding rejection patterns.
		lower := strings.ToLower(errorText)
		if !strings.Contains(lower, "source path") && !strings.Contains(lower, "not backed by") &&
			!strings.Contains(lower, "cites current-workspace") && !strings.Contains(lower, "source-body read") {
			continue
		}

		// Extract paths from the error text — they appear in quoted form:
		// "the answer cites current-workspace path(s) that do not exist: `path1`, `path2`."
		// or "the answer cites source path/line/symbol detail not backed by a source-body read: `path1`."
		var paths []string
		for _, match := range backtickedTokenPattern.FindAllStringSubmatch(errorText, -1) {
			if len(match) > 1 {
				p := strings.TrimSpace(match[1])
				if p != "" {
					paths = append(paths, p)
				}
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}
	return nil
}

func conversationBroadLocalizationAfterCompletionGate(results []workspace.ToolResult) bool {
	afterGate := false

	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool == "completion_gate" {
			afterGate = true
			continue
		}

		if !afterGate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if conversationBroadDiscoveryToolResult(result) {
			return true
		}
	}

	return false
}

func conversationBroadDiscoveryToolResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Output) == "" && strings.TrimSpace(result.Summary) == "" {
		return false
	}

	switch strings.TrimSpace(result.Tool) {
	case "context_pack", "context_query", "context_zoom", "context_impact", "discovery_query", "search_files", "search_content", "structural_search":
		return true
	default:
		return false
	}
}

type conversationDiscoveryRequestRelationKind string

const (
	conversationDiscoveryRequestRelationNone                  conversationDiscoveryRequestRelationKind = "none"
	conversationDiscoveryRequestRelationSourceOwnerRefinement conversationDiscoveryRequestRelationKind = "source_owner_refinement"
	conversationDiscoveryRequestRelationBroadRediscovery      conversationDiscoveryRequestRelationKind = "broad_rediscovery"
)

func conversationBroadDiscoveryRequest(req workspace.ToolRequest) bool {
	return conversationDiscoveryRequestRelation(req) == conversationDiscoveryRequestRelationBroadRediscovery
}

func conversationDiscoveryRequestRelation(req workspace.ToolRequest) conversationDiscoveryRequestRelationKind {
	if conversationSemanticSourceOwnerRefinement(req) {
		return conversationDiscoveryRequestRelationSourceOwnerRefinement
	}

	if conversationTargetedContentSearch(req) ||
		conversationTargetedContextZoom(req) ||
		conversationTargetedFileNameSearch(req) {

		return conversationDiscoveryRequestRelationNone
	}

	if conversationBroadDiscoveryRequestTool(req.Tool) {
		return conversationDiscoveryRequestRelationBroadRediscovery
	}

	return conversationDiscoveryRequestRelationNone
}

func conversationBroadDiscoveryRequestTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "context_pack", "context_query", "context_zoom", "context_impact", "discovery_query", "search_files", "search_content", "structural_search":
		return true
	default:
		return false
	}
}

func conversationSemanticSourceOwnerRefinement(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "context_query" {
		return false
	}

	if strings.TrimSpace(req.Query) == "" || strings.TrimSpace(req.Resolution) == "" {
		return false
	}

	return conversationContextQueryHasConcreteScope(req)
}

func conversationContextQueryHasConcreteScope(req workspace.ToolRequest) bool {
	if conversationConcreteSearchScope(req.Path) ||
		strings.TrimSpace(req.Handle) != "" ||
		strings.TrimSpace(req.ID) != "" ||
		strings.TrimSpace(req.NamedGap) != "" {

		return true
	}

	return false
}

func conversationTargetedContentSearch(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "search_content" {
		return false
	}

	pattern := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query))
	if pattern == "" {
		return false
	}

	return postReadinessGap(req) != "" || conversationConcreteSearchScope(req.Path)
}

func conversationTargetedContextZoom(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "context_zoom" {
		return false
	}

	if strings.TrimSpace(req.Handle) == "" && strings.TrimSpace(req.ID) == "" && strings.TrimSpace(req.Path) == "" {
		return false
	}

	return strings.TrimSpace(req.Resolution) != "" || req.IncludeSource || postReadinessGap(req) != ""
}

func conversationTargetedFileNameSearch(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "search_files" {
		return false
	}

	pattern := util.FirstNonEmpty(req.Pattern, req.Glob, req.Query)

	pattern = filepath.ToSlash(strings.TrimSpace(pattern))

	if pattern == "" {
		return false
	}

	return postReadinessGap(req) != "" || conversationFileSearchPatternIsConcrete(pattern)
}

func conversationConcreteSearchScope(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || path == "." || strings.ContainsAny(path, "*?[") {
		return false
	}

	return pathparse.HasWorkspaceFileExtension(path) || strings.Contains(path, "/")
}

func conversationFileSearchPatternIsConcrete(pattern string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}

	if !strings.ContainsAny(pattern, "*?[") {
		return strings.Contains(pattern, "/") || pathparse.HasWorkspaceFileExtension(pattern)
	}

	for _, token := range agentutil.RecoveryObjectiveSearchWords(pattern, 16) {
		token = strings.ToLower(strings.TrimSpace(token))
		if len(token) < 3 || conversationBroadFileSearchToken(token) {
			continue
		}

		return true
	}

	for _, token := range conversationFileSearchPatternTokens(pattern) {
		if len(token) < 3 || conversationBroadFileSearchToken(token) {
			continue
		}

		return true
	}

	return false
}

func conversationFileSearchPatternTokens(pattern string) []string {
	pattern = strings.ToLower(filepath.ToSlash(strings.TrimSpace(pattern)))
	if pattern == "" {
		return nil
	}

	words := strings.FieldsFunc(pattern, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})

	var out []string

	seen := map[string]struct{}{}

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 3 {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		out = append(out, word)
	}

	return out
}

func conversationBroadFileSearchToken(token string) bool {
	switch token {
	case "cmd", "src", "pkg", "lib", "app", "apps", "test", "tests", "internal", "script", "scripts",
		"go", "rs", "py", "js", "jsx", "ts", "tsx", "java", "cpp", "hpp", "cc", "cxx", "md", "json", "yaml", "yml", "toml":
		return true
	default:
		return false
	}
}

func conversationBroadDiscoveryRejectionRecorded(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		if strings.Contains(strings.ToLower(strings.TrimSpace(result.Summary)), "conversation contract rejected repeated broad discovery") {
			return true
		}
	}

	return false
}

func conversationEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).Answerable()
}


func conversationEvidenceSatisfiedForObjective(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return conversationEvidenceSatisfied(brief, results)
}

func conversationNamedEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	candidates := append([]string{}, brief.TargetRefs...)

	candidates = append(candidates, pathparse.MentionedWorkspaceFiles(brief.Objective)...)

	paths := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}

	for _, candidate := range candidates {
		path := normalizeRunnerPath(candidate)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return paths
}

// conversationInspectedPathSummary returns a compact summary of the
// most recent read/navigation results. It reports evidence without
// declaring readiness — the model owns the decision of when evidence
// is sufficient.
func conversationInspectedPathSummary(results []workspace.ToolResult) string {
	var inspected []string

	for i := len(results) - 1; i >= 0 && len(inspected) < 5; i-- {
		r := results[i]
		if strings.TrimSpace(r.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(r.Tool)
		if tool == "context_pack" || tool == "task" {
			continue
		}

		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			continue
		}

		inspected = append(inspected, summary)
	}

	if len(inspected) == 0 {
		return ""
	}

	// Reverse to show oldest first
	for i, j := 0, len(inspected)-1; i < j; i, j = i+1, j-1 {
		inspected[i], inspected[j] = inspected[j], inspected[i]
	}

	return strings.Join(inspected, "; ")
}

// conversationMilestones derives context-window-aware timing for
// conversation-lane auto-findings capture and call-count awareness nudges.
// Returns (firstMilestone, nudgeInterval) in tool calls.
//
// The derivation mirrors compactionParams in internal/surfaces/tui/app/compaction.go:
// firstMilestone is 25% of the compaction threshold in tool calls,
// nudgeInterval is 35%. Both are floored at current hardcoded defaults
// (15 and 20) so small models keep the existing schedule; large models
// get proportionally later milestones to match their larger context windows.
func conversationMilestones(ctxSize int) (firstMilestone int, nudgeInterval int) {
	if ctxSize <= 0 {
		ctxSize = 65536
	}

	const estTokensPerMsg = 125

	thresholdMsgs := ctxSize * 40 / 100 / estTokensPerMsg

	if thresholdMsgs > 200 {
		thresholdMsgs = 200
	}

	maxToolCalls := thresholdMsgs / 2

	firstMilestone = max(15, maxToolCalls*25/100)
	nudgeInterval = max(20, maxToolCalls*35/100)

	return firstMilestone, nudgeInterval
}

func (r *Runner) conversationEvidenceAvailableForObjective(results []workspace.ToolResult) bool {
	if r == nil || !conversationEvidenceSatisfiedForObjective(r.RuntimeBrief, results) ||
		runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {

		return false
	}

	return true
}

func (r *Runner) mainReadOnlyDelegationBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolRequest{}, false
	}

	if mainReadOnlyDelegationBootstrapEvidenceSeen(results) || !prefersEarlyReadOnlyDelegation(r.RuntimeBrief) || strings.TrimSpace(req.Tool) == "task" || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}

	if !mainReadOnlyObjectiveShouldDelegate(r.RuntimeBrief.Objective) {
		return workspace.ToolRequest{}, false
	}

	if !shouldDelegateMainReadOnlyBootstrapRequest(req) {
		return workspace.ToolRequest{}, false
	}

	if r.Workspace == nil || !containsRunnerTool(r.advertisedAllowedTools(r.Workspace.AllowedTools()), "task") {
		return workspace.ToolRequest{}, false
	}

	prompt := delegatedReadOnlyTaskPrompt(r.RuntimeBrief.Objective, r.Workspace.Root())
	if strings.TrimSpace(prompt) == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:   "task",
		Agent:  "explorer",
		Mode:   "read_only",
		Prompt: prompt,
	}, true
}

func mainReadOnlyObjectiveShouldDelegate(objective string) bool {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	if lower == "" {
		return false
	}

	if mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower) {
		return false
	}

	return containsAny(lower,
		"architecture", "boundary", "boundaries", "flow", "handoff", "lifecycle", "orchestration",
		"how does", "how this", "explain how", "trace", "call", "calls", "invoked", "invoke",
		"subagent", "sub-agent", "sub agent", "child task", "child runner",
		"discovery tool", "discovery tools", "tool surface", "increase", "improve", "improvement", "usefulness", "what can we add",
	)
}

func mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower string) bool {
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "what can we add") || strings.Contains(lower, "improve") || strings.Contains(lower, "usefulness") {
		return false
	}

	if strings.HasPrefix(lower, "what does ") || strings.HasPrefix(lower, "where is ") || strings.HasPrefix(lower, "where does ") {
		return true
	}

	return containsAny(lower, " mode do", " feature do")
}

func mainReadOnlyDelegationBootstrapEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if tool == "task" {
			return true
		}

		if isReadClassTool(workspace.ToolRequest{Tool: tool}) && strings.TrimSpace(result.Output) != "" {
			return true
		}
	}

	return false
}

func (r *Runner) mainReadOnlyBroadSearchContextPackRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolRequest{}, false
	}

	if len(results) > 0 || runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) &&
		!workerruntime.RuntimeContractIsArchitecture(r.RuntimeBrief.Contract) {

		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSearchContent || !containsRunnerTool(r.RuntimeBrief.AllowedTools, workspace.ToolNameContextPack) {
		return workspace.ToolRequest{}, false
	}

	path := normalizeRunnerPath(req.Path)
	if path != "" && path != "." {
		return workspace.ToolRequest{}, false
	}

	pattern := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query))
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	task := strings.TrimSpace(r.RuntimeBrief.Objective)
	if task == "" {
		task = pattern
	}

	return workspace.ToolRequest{Tool: workspace.ToolNameContextPack, Task: task, Mode: "plan", Limit: 8}, true
}

func shouldDelegateMainReadOnlyBootstrapRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextStatus, workspace.ToolNameDiscoveryQuery, workspace.ToolNameHarnessAudit:
		return true
	case workspace.ToolNameListFiles:
		return true
	case workspace.ToolNameSearchFiles:
		pattern := strings.TrimSpace(req.Pattern)
		return pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*")
	case workspace.ToolNameSearchContent:
		path := strings.TrimSpace(req.Path)
		return path == "" || path == "." || !pathparse.HasWorkspaceFileExtension(path)
	default:
		return workspace.IsBroadInspectionRequest(req)
	}
}

func postReadinessSymbolBodyUpgradeRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	tool := strings.TrimSpace(req.Tool)
	if !postReadinessSymbolBodyUpgradeableTool(tool, req.Capability) || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, false
	}

	path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	if !hasTruncatedGoReadSymbolResult(results, path) {
		return workspace.ToolRequest{}, false
	}

	upgraded := req
	if tool == workspace.CodeNavigateToolName {
		upgraded.Capability = workspace.CodeNavigateCapabilityName(workspace.CapExtractBody)
	} else {
		upgraded.Tool = "go_extract_body"
	}

	return upgraded, true
}

func postReadinessSymbolBodyUpgradeableTool(tool, capability string) bool {
	switch strings.TrimSpace(tool) {
	case "go_read_symbol":
		return true
	case workspace.CodeNavigateToolName:
		return workspace.CodeNavigateCapabilityPredicate(workspace.CapReadSymbol)(capability)
	default:
		return false
	}
}

func hasTruncatedGoReadSymbolResult(results []workspace.ToolResult, path string) bool {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if (tool != "go_read_symbol" && tool != workspace.CodeNavigateToolName) || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if tool == workspace.CodeNavigateToolName && !strings.Contains(strings.ToLower(result.Output), "routed_tool: go_read_symbol") {
			continue
		}

		summary := normalizeWorkspacePathScanText(result.Summary + "\n" + result.Output)
		if !strings.Contains(summary, path) {
			continue
		}

		if strings.Contains(strings.ToLower(summary), "full-body adapter: go_extract_body") {
			return true
		}
	}

	return false
}

func (r *Runner) delegatedReadOnlyDiscoveryQueryRepairRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "discovery_query" {
		return workspace.ToolRequest{}, false
	}

	plan := newDelegatedDiscoveryPlan(r.RuntimeBrief.Objective)
	if redirected, ok := plan.candidateConsumptionRequest(results, r.RuntimeBrief.AllowedTools); ok {
		return redirected, true
	}

	repaired, changed := plan.repairDiscoveryQuery(req)
	if r.delegatedReadOnlyDiscoveryPathMissing(repaired.Path) {
		if replacement := r.delegatedReadOnlyFirstExistingDetailedScope(plan); replacement != "" {
			repaired.Path = replacement
			changed = true
		} else if plan.primaryScope != "" && plan.primaryScope != "." {
			repaired.Path = plan.primaryScope
			changed = true
		}
	}

	return repaired, changed
}

func (r *Runner) delegatedReadOnlyPostDiscoveryInventoryRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if redirected, ok := delegatedReadOnlyPostDiscoveryOutlineReadRedirect(results, req); ok {
		return redirected, true
	}

	if !delegatedReadOnlyPostDiscoveryInventoryTool(req.Tool) || len(results) == 0 {
		return workspace.ToolRequest{}, false
	}

	plan := newDelegatedDiscoveryPlan(r.RuntimeBrief.Objective)

	return plan.candidateConsumptionRequest(results, r.RuntimeBrief.AllowedTools)
}

func delegatedReadOnlyPostDiscoveryOutlineReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if len(results) == 0 || len(delegatedReadOnlySourceBodyEvidencePaths(results)) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(req.Capability) != "file_outline" {
		return workspace.ToolRequest{}, false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !delegatedDiscoveryConsumableSourcePath(path) || concreteSourceEvidenceReadForPath(path, results) {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         160,
		NamedGap:        "source-body evidence for delegated read-only synthesis",
		WhyNotAnswering: "the prior discovery query returned source candidates but not source-body evidence",
	}, true
}

func delegatedReadOnlyPostDiscoveryInventoryTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameFileStats, workspace.ToolNameListFiles, workspace.ToolNameSearchFiles:
		return true
	default:
		return false
	}
}

func (r *Runner) delegatedReadOnlyFirstExistingDetailedScope(plan delegatedDiscoveryPlan) string {
	for _, scope := range plan.detailedScopes {
		if scope == "" || r.delegatedReadOnlyDiscoveryPathMissing(scope) {
			continue
		}

		return scope
	}

	return ""
}

func (r *Runner) delegatedReadOnlyDiscoveryPathMissing(path string) bool {
	path = normalizeRunnerPath(path)
	if r == nil || r.Workspace == nil || path == "" || path == "." {
		return false
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}

	if _, err := os.Stat(filepath.Join(root, path)); err != nil {
		return true
	}

	return false
}

func delegatedDiscoveryPurposeValid(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case "impact_analysis", "function_tracing", "architecture_boundary", "security_review", "history_context":
		return true
	default:
		return false
	}
}

func (r *Runner) delegatedReadOnlyBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
			return redirected, true
		}

		return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
	case "search_files":
		pattern := strings.TrimSpace(req.Pattern)
		if pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*") {
			if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
				return redirected, true
			}

			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	case "search_content":
		path := strings.TrimSpace(req.Path)
		if path == "" || path == "." || !pathparse.HasWorkspaceFileExtension(path) {
			if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
				return redirected, true
			}

			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	}

	return workspace.ToolRequest{}, false
}

func (r *Runner) delegatedReadOnlyBootstrapQueryRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) == "discovery_query" || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}

	if !containsRunnerTool(r.RuntimeBrief.AllowedTools, "discovery_query") {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyBootstrapHasExactStructuredProbe(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools) {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyObjectiveHasExactPrimarySourceAnchor(r.RuntimeBrief.Objective) &&
		!delegatedReadOnlyCompositeQuestionOverridesExactSourceAnchor(r.RuntimeBrief.Objective) {

		return workspace.ToolRequest{}, false
	}

	if !delegatedReadOnlyObjectiveRequiresDiscoveryQuery(r.RuntimeBrief.Objective) {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:     "discovery_query",
		Purpose:  delegatedReadOnlyDiscoveryPurpose(r.RuntimeBrief.Objective),
		Path:     delegatedReadOnlyBootstrapScope(r.RuntimeBrief.Objective),
		Pattern:  pattern,
		MaxDepth: 2,
	}, true
}

func delegatedReadOnlyObjectiveRequiresDiscoveryQuery(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	// This classifier only identifies broad child-runner mechanism questions
	// that need composite discovery. Exact source anchors are preserved by
	// delegatedReadOnlyBootstrapQueryRequest before this predicate is consulted.
	return containsAny(lower,
		"discovery tool",
		"discovery tools",
		"tool usefulness",
		"increase their usefulness",
		"overall usefulness",
		"subagent",
		"sub-agent",
		"sub agent",
		"child-agent",
		"child agent",
		"child task",
		"task tool",
		"delegation",
	)
}

func delegatedReadOnlyObjectiveHasExactPrimarySourceAnchor(objective string) bool {
	for _, path := range pathparse.MentionedWorkspacePaths(objective) {
		path = normalizeRunnerPath(path)
		if path == "" || delegatedReadOnlySecondaryEvidencePath(path) || delegatedReadOnlyMentionedPathIsPattern(path) {
			continue
		}

		if pathparse.HasWorkspaceFileExtension(path) {
			return true
		}
	}

	return false
}

func delegatedReadOnlyCompositeQuestionOverridesExactSourceAnchor(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	return containsAny(lower, "discovery tool", "discovery tools", "tool usefulness", "increase their usefulness", "overall usefulness")
}

func delegatedReadOnlyMentionedPathIsPattern(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	base := filepath.Base(path)
	if strings.ContainsAny(base, "*?[]") {
		return true
	}

	return strings.HasPrefix(path, ".") && !strings.Contains(strings.TrimPrefix(path, "."), "/")
}

func delegatedReadOnlyChildMechanismQuestion(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(delegatedDisplayQuestion(objective)), " "))
	return containsAny(lower,
		"subagent",
		"sub-agent",
		"sub agent",
		"child-agent",
		"child agent",
		"child task",
		"task tool",
		"delegation",
	)
}

func delegatedReadOnlyDiscoveryPurpose(objective string) string {
	return newDelegatedDiscoveryPlan(objective).defaultPurpose
}

func delegatedReadOnlyTaskPrompt(objective string, workspaceRoot string) string {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return ""
	}

	parts := []string{objective}
	if scope := delegatedReadOnlyLikelySourceScope(objective, workspaceRoot); scope != "" {
		parts = append(parts, "Named workspace scope: "+scope)
	}

	parts = append(parts,
		"Start from source-of-truth implementation files before tests, README, or docs.",
		"Return inspected paths, key findings, confidence, and named gaps.",
	)

	return strings.Join(parts, "\n")
}

func delegatedReadOnlyLikelySourceScope(objective string, workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return ""
	}

	scopes := make([]string, 0, 6)

	for _, candidate := range delegatedReadOnlyLikelySourceCandidates(objective) {
		info, err := os.Stat(filepath.Join(workspaceRoot, candidate))
		if err != nil || !info.IsDir() {
			continue
		}

		scopes = append(scopes, candidate)
	}

	return strings.Join(scopes, ", ")
}

func delegatedReadOnlyLikelySourceCandidates(objective string) []string {
	if delegatedObjectiveNeedsEntrypointScope(objective) {
		return []string{"internal/runtime/agent", "internal/capabilities/workspace", "cmd", "internal", "pkg", "src"}
	}

	return []string{"internal/runtime/agent", "internal/capabilities/workspace", "internal", "pkg", "src"}
}

func delegatedObjectiveNeedsEntrypointScope(objective string) bool {
	lower := strings.ToLower(objective)

	return containsAny(lower,
		"entrypoint",
		"entry point",
		"main entry",
		"main function",
		"cli",
		"command",
		"startup",
		"boot sequence",
		"bootstrap path",
		"program start",
		"cmd directory",
		"cmd/",
	)
}

func delegatedReadOnlyBootstrapSearchRequest(objective string) (workspace.ToolRequest, bool) {
	pattern := delegatedReadOnlyBootstrapPattern(objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	req := workspace.ToolRequest{Tool: "search_content", Pattern: pattern}
	if scope := delegatedReadOnlyBootstrapScope(objective); scope != "" && scope != "." {
		req.Path = scope
	}

	if !pathparse.HasWorkspaceFileExtension(req.Path) {
		req.Glob = "*.go"
	}

	return req, true
}

func delegatedReadOnlyBootstrapStructuredRequest(objective string, allowedTools []string) (workspace.ToolRequest, bool) {
	if !containsRunnerTool(allowedTools, "go_workspace_symbol") {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapSymbolPattern(objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: pattern}, true
}

func delegatedReadOnlyBootstrapHasExactStructuredProbe(objective string, allowedTools []string) bool {
	_, ok := delegatedReadOnlyBootstrapStructuredRequest(objective, allowedTools)
	return ok
}

func delegatedReadOnlyBootstrapScope(objective string) string {
	return newDelegatedDiscoveryPlan(objective).primaryScope
}

func delegatedReadOnlyBroadNamedScope(scope string) bool {
	switch normalizeRunnerPath(scope) {
	case "internal", "pkg", "src", "cmd", "prompts", "docs":
		return true
	default:
		return false
	}
}

func delegatedReadOnlyBootstrapPattern(objective string) string {
	return newDelegatedDiscoveryPlan(objective).searchPattern
}

func delegatedReadOnlyBootstrapSymbolPattern(objective string) string {
	return newDelegatedDiscoveryPlan(objective).symbolPattern
}

func delegatedReadOnlySearchTerm(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))

	if value == "" || pathparse.LooksLikeWorkspacePathToken(value) {
		return "", false
	}

	if delegatedDiscoveryStopWord(value, delegatedDiscoveryStopWordSearchTerm) {
		return "", false
	}

	if len(value) < 4 {
		return "", false
	}

	var b strings.Builder

	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteString(regexp.QuoteMeta(string(r)))
		case r == '-', r == '_', unicode.IsSpace(r):
			b.WriteString("[-_ ]?")
		}
	}

	pattern := strings.TrimSpace(b.String())
	if pattern == "" {
		return "", false
	}

	return pattern, true
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

func (r *Runner) delegatedReadOnlySecondaryEvidenceRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	targetPath := r.delegatedReadOnlyRequestPath(req)
	if targetPath == "" || !delegatedReadOnlySecondaryEvidencePath(targetPath) {
		return workspace.ToolRequest{}, false
	}

	if redirected, ok := delegatedReadOnlyNamedPrimaryPathRedirect(r.RuntimeBrief.Objective, targetPath); ok {
		return redirected, true
	}

	if strings.HasSuffix(targetPath, "_test.go") {
		sourcePath := strings.TrimSuffix(targetPath, "_test.go") + ".go"
		pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, sourcePath)

		if pattern != "" {
			return workspace.ToolRequest{Tool: "search_content", Path: sourcePath, Pattern: pattern}, true
		}
	}

	return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
}

func (r *Runner) delegatedReadOnlyRequestPath(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}

	if path := strings.TrimSpace(req.Path); path != "" {
		return filepath.ToSlash(path)
	}

	if r.Workspace == nil || strings.TrimSpace(req.ID) == "" {
		return ""
	}

	path, err := r.Workspace.ResolveHandlePath(req.ID)
	if err != nil {
		return ""
	}

	return filepath.ToSlash(path)
}

func delegatedReadOnlySecondaryEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	lower := strings.ToLower(path)
	if lower == ".codex/agents" || strings.HasPrefix(lower, ".codex/agents/") {
		return true
	}

	if strings.HasSuffix(lower, "_test.go") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") {
		return true
	}

	base := strings.ToLower(filepath.Base(lower))

	return base == "readme.md" || strings.HasSuffix(base, ".md")
}

func delegatedReadOnlyNamedPrimaryPathRedirect(objective string, blockedPath string) (workspace.ToolRequest, bool) {
	blockedPath = filepath.ToSlash(strings.TrimSpace(blockedPath))
	for _, candidate := range pathparse.MentionedWorkspacePaths(objective) {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if candidate == "" || candidate == blockedPath || delegatedReadOnlySecondaryEvidencePath(candidate) {
			continue
		}

		pattern := delegatedReadOnlyImplementationPattern(objective, candidate)
		if pattern == "" {
			continue
		}

		return workspace.ToolRequest{Tool: "search_content", Path: candidate, Pattern: pattern}, true
	}

	return workspace.ToolRequest{}, false
}

func delegatedReadOnlyImplementationPattern(objective string, targetPath string) string {
	terms := make([]string, 0, 8)

	for _, part := range strings.Split(delegatedReadOnlyBootstrapPattern(objective), "|") {
		if delegatedReadOnlyImplementationAliasPattern(part) {
			continue
		}

		terms = util.AppendUnique(terms, part)
	}

	base := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))

	base = strings.TrimSuffix(base, "_test")

	for _, fragment := range strings.FieldsFunc(base, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	}) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = util.AppendUnique(terms, term)
		}
	}

	return strings.Join(terms, "|")
}

func delegatedReadOnlyImplementationAliasPattern(part string) bool {
	part = strings.ToLower(strings.TrimSpace(part))
	switch part {
	case "child[-_ ]?task", "task[-_ ]?tool", "delegate", "delegation":
		return true
	default:
		return false
	}
}

func delegatedReadOnlyMechanismTerms(objective string) []string {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	terms := make([]string, 0, 6)

	if containsAny(lower, "invoke", "invoked", "execute", "executed", "run", "runner", "workflow", "wired") {
		for _, term := range []string{"invoke", "execute"} {
			terms = util.AppendUnique(terms, term)
		}
	}

	if containsAny(lower, "subagent", "sub-agent", "sub agent") {
		for _, term := range []string{"child task", "task tool", "delegate", "delegation"} {
			terms = util.AppendUnique(terms, term)
		}
	}

	return terms
}

func (r *Runner) delegatedReadOnlyNamedFileReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, false
	}

	path := strings.TrimSpace(req.Path)
	if !strings.HasSuffix(path, ".go") {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	if containsRunnerTool(r.RuntimeBrief.AllowedTools, "go_file_outline") {
		return workspace.ToolRequest{Tool: "go_file_outline", Path: path}, true
	}

	return workspace.ToolRequest{Tool: "search_content", Path: path, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlyIntegrationPivotRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case "read_file", "search_content":
	default:
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyClosureReady(r.agentName(), r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}

	currentPath := r.delegatedReadOnlyRequestPath(req)
	if currentPath == "" || !strings.HasSuffix(currentPath, ".go") || strings.HasSuffix(currentPath, "_test.go") {
		return workspace.ToolRequest{}, false
	}

	if !delegatedReadOnlyHasBodyRead(results, currentPath) {
		return workspace.ToolRequest{}, false
	}

	targetPath := delegatedReadOnlySiblingImplementationPath(results, currentPath)
	if targetPath == "" || targetPath == currentPath {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyHasBodyRead(results, targetPath) {
		return workspace.ToolRequest{}, false
	}

	if start, end, ok := delegatedReadOnlySearchHitWindow(results, targetPath); ok {
		return workspace.ToolRequest{
			Tool:      "read_file",
			Path:      targetPath,
			StartLine: start,
			EndLine:   end,
		}, true
	}

	pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, targetPath)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: "search_content", Path: targetPath, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlySearchHitReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolRequest{}, false
	}

	path := r.delegatedReadOnlyRequestPath(req)
	if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return workspace.ToolRequest{}, false
	}

	start, end, ok := delegatedReadOnlySearchHitWindow(results, path)
	if !ok {
		return workspace.ToolRequest{}, false
	}

	if req.StartLine <= 0 || req.EndLine <= 0 {
		return workspace.ToolRequest{}, false
	}

	if req.StartLine == start && req.EndLine == end {
		return workspace.ToolRequest{}, false
	}

	if start >= req.StartLine-20 && end <= req.EndLine+20 {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: start,
		EndLine:   end,
	}, true
}

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

	tool := strings.TrimSpace(util.FirstNonEmpty(result.Tool, req.Tool))
	closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)

	switch {
	case tool == "discovery_query":
		if !delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
			return false
		}

		// A composite discovery packet provides a codebase map but not
		// source content. Require at least one source body read before
		// declaring readiness, otherwise the model is blocked from
		// reading the files the discovery query surfaced.
		if !closure.Answerable() {
			return false
		}
	case isReadClassTool(workspace.ToolRequest{Tool: tool}):
		if !closure.Answerable() {
			return false
		}
	default:
		return false
	}

	return true
}

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

	if r.conversationEvidenceAvailableForObjective(results) {
		return true
	}

	if !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return false
	}

	exactProbes := readOnlyAnalysisExactProbeCount(results)
	evidencePaths := promptEvidencePathsFromToolResults(results, 6)

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)
		return exactProbes >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.RuntimeBrief, closure.Evidence)
	}

	closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)

	return exactProbes >= 2 && len(evidencePaths) >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.RuntimeBrief, closure.Evidence)
}

func readOnlyAnalysisHasObjectiveRelevantSourceBody(brief workerruntime.WorkerRuntimeBrief, evidence readOnlyAnswerabilityLedger) bool {
	if len(evidence.SourceBodyObjectivePaths) > 0 {
		return true
	}

	for _, path := range evidence.SourceBodyPaths {
		if implementationSourcePathOverlapsObjective(path, brief.Objective) ||
			sourceEvidencePathSharesObjectiveTerm(path, brief.Objective) {

			return true
		}
	}

	return false
}

func readOnlyAnalysisExactProbeCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if agentutil.CandidateAnchorOnlyResult(result) {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case "read_file":
			if readFileResultOnlyMentionsDiagnosticLogArtifacts(result) {
				continue
			}

			if span := toolResultReadSpan(result); span > 0 && span <= 120 {
				count++
			}
		case "code_navigate":
			if agentutil.RoutedSourceReadOutputHasBody(result.Output) &&
				!strings.Contains(result.Output, "capability: file_outline") {

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

func delegatedReadOnlyInspectedImplementationPaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if agentutil.CandidateAnchorOnlyResult(result) {
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

func delegatedReadOnlyHasBodyRead(results []workspace.ToolResult, wantPath string) bool {
	wantPath = filepath.ToSlash(strings.TrimSpace(wantPath))
	if wantPath == "" {
		return false
	}

	for _, path := range delegatedReadOnlyInspectedImplementationPaths(results) {
		if path == wantPath {
			return true
		}
	}

	return false
}

func delegatedReadOnlySiblingImplementationPath(results []workspace.ToolResult, currentPath string) string {
	currentPath = filepath.ToSlash(strings.TrimSpace(currentPath))
	if currentPath == "" {
		return ""
	}

	candidates := make([]string, 0, 6)
	dir := filepath.ToSlash(filepath.Dir(currentPath))
	base := strings.TrimSuffix(filepath.Base(currentPath), filepath.Ext(currentPath))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' })

	for shrink := len(parts) - 1; shrink >= 1; shrink-- {
		candidate := filepath.ToSlash(filepath.Join(dir, strings.Join(parts[:shrink], "_")+".go"))
		if candidate != currentPath {
			candidates = append(candidates, candidate)
		}
	}

	for _, path := range pinnedPathsFromToolResults(results, 32) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == currentPath || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			continue
		}

		if filepath.ToSlash(filepath.Dir(path)) != dir {
			continue
		}

		candidates = append(candidates, path)
	}

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}

		seen[candidate] = struct{}{}
		for _, result := range results {
			if toolResultMentionsPath(result, candidate) {
				return candidate
			}
		}
	}

	return ""
}

func delegatedReadOnlySearchHitWindow(results []workspace.ToolResult, path string) (int, int, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return 0, 0, false
	}

	pattern := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		matches := pattern.FindStringSubmatch(result.Output)
		if len(matches) != 2 {
			continue
		}

		line, err := strconv.Atoi(matches[1])
		if err != nil || line <= 0 {
			continue
		}

		start := line - 20
		if start < 1 {
			start = 1
		}

		return start, line + 80, true
	}

	return 0, 0, false
}

func toolResultMentionsPath(result workspace.ToolResult, path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	for _, field := range []string{result.Summary, result.Output, result.Error} {
		if strings.Contains(filepath.ToSlash(field), path) {
			return true
		}
	}

	return false
}
