package agent

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// DiscoveryRedirect inspects an about-to-run tool request and, if a more
// deterministic language-specific tool exists for the same question,
// returns a synthesized redirect ToolResult that points the model at the
// better tool. A second identical call (same fingerprint) is allowed
// through as the fallback path — the model gets one nudge per
// (fingerprint, turn), never a lockout.
//
// The redirect layer reasons in capability space (workspace.Capability)
// rather than tool-name space, so adding a new language only requires a
// LanguageSupport entry in the workspace registry — no changes here.
type DiscoveryRedirect struct {
	// firedRedirects keys on the redirect fingerprint (tool+Path+Pattern).
	// The first time a fingerprint fires, we synthesize a redirect. The
	// second time, we execute normally — the model has committed to the
	// generic path and we respect that rather than looping forever.
	firedRedirects map[string]bool
	// decisionTablesShown keys on language name. Ensures the full
	// decision guide is injected once per (language, turn), not on every
	// redirect.
	decisionTablesShown map[string]bool
	// kindFireCount keys on "lang|capability" and counts how many times a
	// given redirect class has fired this turn. Repeated redirects make
	// the guidance more explicit, but remain soft so the redirect layer
	// does not become a read-budget stop.
	kindFireCount map[string]int
	// fingerprintFireCount keys on the redirect fingerprint. Tracks how
	// many times this exact (tool+path+pattern) fingerprint has fired a
	// redirect this turn. The fallthrough gating uses this per-fingerprint
	// counter, not the kind-level counter, so that different patterns that
	// share the same (language, capability) class don't jointly exhaust
	// the fallthrough budget for each other.
	fingerprintFireCount map[string]int
	// allowedTools is the active lane/runtime surface. When a
	// language-specific direct tool is present in the workspace manager
	// but hidden by the lane contract, redirects must point at an
	// advertised wrapper such as code_navigate instead of leaking the
	// hidden direct tool name as the next candidate request.
	allowedTools []string
}

// smallFileRedirectThreshold is the line count below which read_file on
// a Go/C++ file skips the outline redirect and executes directly. Based
// on log analysis: files under this size almost always get read
// whole-file in the follow-up step anyway, so the outline is pure
// overhead. Larger files benefit from outline because the model picks
// a specific symbol to go_read_symbol next.
//
// Threshold chosen from SWE suite empirical: 29% of tasks hit
// redirect_storm at the 200-line threshold. Most .go files in typical
// repos are under 400 lines — raising here cuts the outline detour
// for the majority while still protecting the large-file case.
const smallFileRedirectThreshold = 400

// NewDiscoveryRedirect returns a per-turn redirect state.
func NewDiscoveryRedirect() *DiscoveryRedirect {
	return &DiscoveryRedirect{
		firedRedirects:      make(map[string]bool),
		decisionTablesShown: make(map[string]bool),
		kindFireCount:          make(map[string]int),
		fingerprintFireCount:  make(map[string]int),
	}
}

func (d *DiscoveryRedirect) SetAllowedTools(tools []string) {
	if d == nil {
		return
	}
	d.allowedTools = append(d.allowedTools[:0], tools...)
}

// RedirectDecision is the outcome of a Consider call. When ShouldRedirect
// is false the runner proceeds with the original request untouched.
type RedirectDecision struct {
	ShouldRedirect bool
	// Result is the synthesized ToolResult delivered back to the model
	// in place of executing the original request.
	Result workspace.ToolResult
	// DecisionGuide, if non-empty, is a one-shot system message the
	// runner should append to the turn's message list. This carries the
	// language's full capability list and fires at most once per
	// (language, turn).
	DecisionGuide string
	// Language is the language matched by the redirect (for telemetry).
	Language string
	// Capability is the capability the redirect recommends (for telemetry).
	Capability workspace.Capability
	// HardReject signals that the caller should treat this as a
	// rejection (populate its rejectedFingerprints, bump counters)
	// rather than as a soft "redirected" result. Generic code-read
	// redirects intentionally leave this false: they are guidance, not
	// permission invariants.
	HardReject bool
}

// Consider inspects a request and returns a redirect decision. When the
// request is already a language-specific tool, is not covered by any
// rule, or has already fired a redirect this turn, the decision has
// ShouldRedirect=false.
//
// ws is used to confirm the language is actually active in the current
// workspace — we never redirect to a tool that does not apply.
func (d *DiscoveryRedirect) Consider(req workspace.ToolRequest, ws *workspace.Manager) RedirectDecision {
	if d == nil || ws == nil {
		return RedirectDecision{}
	}
	if isStructuredTool(req.Tool) {
		return RedirectDecision{}
	}
	// Shell has its own handler because some replacements (sed -n → read_file)
	// aren't language-specific — they don't fit the LanguageSupport pipeline.
	if strings.EqualFold(strings.TrimSpace(req.Tool), "shell") {
		return d.considerShell(req, ws)
	}
	lang, cap, ok := matchRedirectRule(req, ws)
	if !ok {
		return RedirectDecision{}
	}
	if cap == workspace.CapFileOutline && strings.TrimSpace(req.Path) == "" {
		if path, ok := redirectReadFilePath(req, ws); ok {
			req.Path = path
		}
	}
	req = normalizeRedirectRequest(req, lang, cap)
	replacement := lang.Tool(cap)
	if replacement == "" {
		return RedirectDecision{}
	}
	adapter := d.redirectAdapter(replacement)
	fp := redirectFingerprint(req)
	kind := lang.Name + "|" + string(cap)
	priorKindFires := d.kindFireCount[kind]

	// Increment the per-fingerprint counter first so the fallthrough
	// check uses this call's count. Call 1 (fpFires=1): redirect.
	// Call 2 (fpFires=2): fall through — honor the model's retry.
	// Calls 3+ (fpFires>=3): redirect again with escalated messaging.
	fpFires := d.fingerprintFireCount[fp] + 1
	d.fingerprintFireCount[fp] = fpFires
	if fpFires == 2 {
		// Model retried the same call after one redirect — honor it.
		return RedirectDecision{}
	}
	d.firedRedirects[fp] = true
	d.kindFireCount[kind] = priorKindFires + 1

	guide := ""
	if !d.decisionTablesShown[lang.Name] {
		d.decisionTablesShown[lang.Name] = true
		guide = lang.DecisionGuide
	}

	// After turnWideReadFileHardBlockAt whole-file outline redirects of the
	// same language, flip wide raw reads to a hard-reject path so the runner
	// populates rejectedFingerprints and a retry terminates the churn. Gated
	summary := redirectSummary(req, adapter.Tool)
	msg := redirectMessage(req, lang, cap, replacement, adapter)
	if priorKindFires >= 1 {
		msg = escalateRedirectMessage(msg, lang, cap, adapter.Label(), priorKindFires+1)
	}
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  msg,
	}
	return RedirectDecision{
		ShouldRedirect: true,
		Result:         result,
		DecisionGuide:  guide,
		Language:       lang.Name,
		Capability:     cap,
	}
}

type redirectAdapter struct {
	Tool       string
	Underlying string
}

func (a redirectAdapter) Label() string {
	if strings.TrimSpace(a.Tool) == "code_navigate" {
		return "code_navigate"
	}
	if strings.TrimSpace(a.Underlying) != "" && strings.TrimSpace(a.Underlying) != strings.TrimSpace(a.Tool) {
		return fmt.Sprintf("%s (underlying %s)", strings.TrimSpace(a.Tool), strings.TrimSpace(a.Underlying))
	}
	return strings.TrimSpace(a.Tool)
}

func (d *DiscoveryRedirect) redirectAdapter(replacement string) redirectAdapter {
	replacement = strings.TrimSpace(replacement)
	adapter := redirectAdapter{Tool: replacement}
	if replacement == "" || len(d.allowedTools) == 0 || redirectToolAllowed(d.allowedTools, replacement) {
		return adapter
	}
	if redirectToolAllowed(d.allowedTools, "code_navigate") {
		return redirectAdapter{Tool: "code_navigate", Underlying: replacement}
	}
	return adapter
}

func redirectToolAllowed(tools []string, tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	for _, candidate := range tools {
		if strings.TrimSpace(candidate) == tool {
			return true
		}
	}
	return false
}

func redirectSummary(req workspace.ToolRequest, replacement string) string {
	replacement = strings.TrimSpace(replacement)
	path := strings.TrimSpace(req.Path)
	if path != "" {
		return fmt.Sprintf("Redirected %s to %s", path, replacement)
	}
	return fmt.Sprintf("Redirected to %s", replacement)
}

// escalateRedirectMessage prepends a hardened preface to the standard
// redirect body when the same (language, capability) class has already
// fired this turn. The number of prior fires is included so the model
// sees the streak, not just an anonymous scolding.
func escalateRedirectMessage(base string, lang workspace.LanguageSupport, cap workspace.Capability, replacement string, fireNumber int) string {
	preface := fmt.Sprintf(
		"This exact call has been redirected %d times this turn. The structured tool route via %s for %s code queries remains available. If the structured tool already returned results for this symbol, those results are your answer.\n\n",
		fireNumber, replacement, lang.Name,
	)
	return preface + base
}

func redirectMessage(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability, replacement string, adapter redirectAdapter) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This %s code query is answered through the %s capability via %s.\n",
		lang.Name, cap, adapter.Label())
	switch cap {
	case workspace.CapFileOutline:
		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, req.Path, "")
		readSym := lang.Tool(workspace.CapReadSymbol)
		fmt.Fprintf(&b, "related_capability: read_symbol via %s returns one declaration body by name.\n", relatedRedirectAdapterLabel(adapter, readSym))
		b.WriteString("raw_range_fallback: read_file with start_line/end_line remains appropriate for ranges a single declaration cannot cover.\n")
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol:
		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, req.Path, req.Pattern)
		b.WriteString("The structured result includes file, line, kind, and qualified name.\n")
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s returns a hit's body by pattern and path; raw range fallback remains for context beyond a single declaration.\n", relatedRedirectAdapterLabel(adapter, readSym))
		}
	case workspace.CapReferences:
		path := req.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, path, req.Pattern)
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s can inspect each hit's enclosing declaration; raw range fallback remains for surrounding context.\n", relatedRedirectAdapterLabel(adapter, readSym))
		}
	default:
		// Generic hint for capabilities that don't have a bespoke
		// template above. Adding a new Capability should not silently
		// degrade this message — the hint always names the tool so the
		// model can construct the call from its schema.
		fmt.Fprintf(&b, "candidate_adapter: %s with equivalent arguments from the rejected request.\n", adapter.Label())
	}
	b.WriteString("The structured route remains available. This redirect has fired for this exact fingerprint this turn.")
	return strings.TrimSpace(b.String())
}

func relatedRedirectAdapterLabel(primary redirectAdapter, directRelated string) string {
	if strings.TrimSpace(primary.Tool) == "code_navigate" {
		return "code_navigate"
	}
	return strings.TrimSpace(directRelated)
}

func writeRedirectCandidateRequest(b *strings.Builder, adapter redirectAdapter, cap workspace.Capability, language, path, pattern string) {
	if b == nil {
		return
	}
	if strings.TrimSpace(adapter.Tool) == "code_navigate" {
		fmt.Fprintf(b, "candidate_request: {\"tool\":\"code_navigate\",\"capability\":%q", string(cap))
		if strings.TrimSpace(path) != "" {
			fmt.Fprintf(b, ",\"path\":%q", path)
		}
		if strings.TrimSpace(pattern) != "" {
			fmt.Fprintf(b, ",\"pattern\":%q", pattern)
		}
		if strings.TrimSpace(language) != "" {
			fmt.Fprintf(b, ",\"language\":%q", language)
		}
		b.WriteString("}\n")
		return
	}
	fmt.Fprintf(b, "candidate_request: {\"tool\":%q", adapter.Tool)
	if strings.TrimSpace(pattern) != "" {
		fmt.Fprintf(b, ",\"pattern\":%q", pattern)
	}
	if strings.TrimSpace(path) != "" {
		fmt.Fprintf(b, ",\"path\":%q", path)
	}
	b.WriteString("}\n")
}

func normalizeRedirectRequest(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability) workspace.ToolRequest {
	switch cap {
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol, workspace.CapReferences:
		if ident, ok := extractSyntaxIdentifierForLanguage(lang.Name, req.Pattern); ok {
			req.Pattern = ident
		}
	}
	return req
}

func redirectFingerprint(req workspace.ToolRequest) string {
	return req.Tool + "|" + req.ID + "|" + req.Path + "|" + req.Pattern + "|" + req.Glob + "|" + req.Command
}

func redirectReadFilePath(req workspace.ToolRequest, ws *workspace.Manager) (string, bool) {
	path := strings.TrimSpace(req.Path)
	if path != "" {
		return path, true
	}
	id := strings.TrimSpace(req.ID)
	if id == "" || ws == nil {
		return "", false
	}
	resolved, err := ws.ResolveHandlePath(id)
	if err != nil {
		return "", false
	}
	if rel, err := filepath.Rel(ws.Root(), resolved); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel), true
	}
	return resolved, true
}

// matchRedirectRule maps a generic tool request to a (language,
// capability) pair when a deterministic tool would answer the same
// question better. Returns ok=false when no rule matches.
func matchRedirectRule(req workspace.ToolRequest, ws *workspace.Manager) (workspace.LanguageSupport, workspace.Capability, bool) {
	switch strings.ToLower(strings.TrimSpace(req.Tool)) {
	case "read_file":
		// A read_file on a code file with no line range is almost
		// always "what's in this file?" — file_outline answers that
		// with line ranges the model can use for a follow-up bounded
		// read_file. A read with start_line/end_line set is a
		// targeted read and is not redirected.
		if req.StartLine > 0 || req.EndLine > 0 {
			return workspace.LanguageSupport{}, "", false
		}
		path, ok := redirectReadFilePath(req, ws)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		lang, ok := workspace.LanguageForFile(path)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		if !languageActive(ws, lang) {
			return workspace.LanguageSupport{}, "", false
		}
		if lang.Tool(workspace.CapFileOutline) == "" {
			return workspace.LanguageSupport{}, "", false
		}
		lineCount, ok := ws.FileLineCount(path)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		// Small files short-circuit the outline detour. A typical
		// outline+read_symbol loop costs two tool calls plus a chunk
		// of prompt context for the outline table; below this
		// threshold, reading the whole file directly is cheaper AND
		// more informative (the model needs the bodies anyway). This
		// applies to tests too: small tests are cheap to read, but a
		// multi-thousand-line test file needs an outline before a raw
		// dump can blow the planner context budget.
		// The threshold is deliberately generous — outline is still the
		// right answer for 500-line files with many decls.
		if lineCount > 0 && lineCount <= smallFileRedirectThreshold {
			return workspace.LanguageSupport{}, "", false
		}
		return lang, workspace.CapFileOutline, true
	case "search_content":
		pattern := strings.TrimSpace(req.Pattern)
		if pattern == "" {
			return workspace.LanguageSupport{}, "", false
		}
		if searchContentTargetsConcreteFile(req) && looksLikeIdentifier(pattern) {
			return workspace.LanguageSupport{}, "", false
		}
		lang, ok := languageForSearchRequest(req, ws)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		if !languageActive(ws, lang) {
			return workspace.LanguageSupport{}, "", false
		}
		// Choose between workspace-wide and path-scoped symbol search
		// based on request intent, not purely on which tool the language
		// exposes. When the model already scoped the request to a path,
		// the index-backed find_symbol respects that scope directly;
		// workspace_symbol (gopls-backed) would ignore it and return
		// unfiltered hits. When no path is set, workspace_symbol is the
		// right default — it has the broader reach. Either tool falls
		// back to the other when the preferred one is missing.
		primary, secondary := workspace.CapWorkspaceSymbol, workspace.CapFindSymbol
		if strings.TrimSpace(req.Path) != "" {
			primary, secondary = workspace.CapFindSymbol, workspace.CapWorkspaceSymbol
		}
		cap := primary
		if lang.Tool(cap) == "" {
			cap = secondary
			if lang.Tool(cap) == "" {
				return workspace.LanguageSupport{}, "", false
			}
		}
		return lang, cap, true
	}
	return workspace.LanguageSupport{}, "", false
}


// considerConversationRedirect handles conversation-lane redirects from
// generic text-search tools to structured discovery tools. The model
// defaults to search_content (grep) because it is familiar; this redirect
// converts the search pattern into a context_query graph question, which
// answers the same need with structured results instead of raw line hits.
//
// Only fires in conversation/read-only lanes. Follows the same
// one-redirect-per-fingerprint pattern as language redirects.
func (d *DiscoveryRedirect) considerConversationRedirect(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) RedirectDecision {
	if d == nil {
		return RedirectDecision{}
	}
	// Only for conversation and read-only lanes
	if !strings.EqualFold(strings.TrimSpace(brief.TaskFamily), "conversation") &&
		!strings.EqualFold(strings.TrimSpace(brief.Contract), "direct_answer") &&
		!strings.EqualFold(strings.TrimSpace(brief.Contract), "grounded_evidence_summary") {
		return RedirectDecision{}
	}
	tool := strings.TrimSpace(req.Tool)
	if tool != "search_content" {
		return RedirectDecision{}
	}
	// Don't redirect if context_query isn't available
	if !slices.Contains(d.allowedTools, "context_query") {
		return RedirectDecision{}
	}
	fp := redirectFingerprint(req)
	fpFires := d.fingerprintFireCount[fp] + 1
	d.fingerprintFireCount[fp] = fpFires
	if fpFires == 2 {
		// Model retried the same search after one redirect — honor it.
		return RedirectDecision{}
	}
	d.firedRedirects[fp] = true

	// Convert the search pattern into a context_query question
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		pattern = strings.TrimSpace(req.Command)
	}
	if pattern == "" {
		return RedirectDecision{}
	}

	query := fmt.Sprintf("find references, definitions, or uses of %s", pattern)
	contextQueryRequest := fmt.Sprintf(
		`{"tool":"context_query","query":%q,"resolution":"symbol"}`,
		query,
	)

	summary := "Redirected search_content to context_query"
	msg := "Instead of raw text search, use context_query for structured graph questions. " +
		"The same investigation is better served by:\n\n" +
		contextQueryRequest + "\n\n" +
		"context_query returns callers, definitions, and references from the codebase graph " +
		"without dumping raw file content. You can refine the query in natural language. " +
		"If context_query returns nothing, you can retry search_content as a fallback."

	return RedirectDecision{
		ShouldRedirect: true,
		Result: workspace.ToolResult{
			Tool:    req.Tool,
			Summary: summary,
			Output:  msg,
		},
	}
}
